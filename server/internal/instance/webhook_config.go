package instance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
)

const (
	managedWebhookName       = "Cantinarr"
	managedWebhookUsername   = "cantinarr"
	maxArrConfigurationBytes = 2 << 20
)

// ConfigureWebhook installs or updates Cantinarr's server-managed Connect →
// Webhook record in a Radarr/Sonarr instance. The callback credential never
// crosses the Cantinarr client API; it moves only from this server to the arr.
func (h *Handler) ConfigureWebhook(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceID")
	unlock := h.lockWebhookConfiguration(instanceID)
	defer unlock()
	inst, err := h.store.Get(instanceID)
	if err != nil {
		http.Error(w, `{"error":"failed to get instance"}`, http.StatusInternalServerError)
		return
	}
	if inst == nil {
		http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
		return
	}
	if !SupportsManagedWebhook(inst.ServiceType) {
		http.Error(w, `{"error":"webhooks are supported only for radarr, sonarr, and chaptarr"}`, http.StatusBadRequest)
		return
	}

	callbackURL, err := h.arrWebhookCallbackURL(r, instanceID)
	if err != nil {
		http.Error(w, `{"error":"could not determine the public Cantinarr URL"}`, http.StatusBadRequest)
		return
	}
	// Prepare a credential accepted alongside the current one. Failed or
	// ambiguous arr I/O leaves both valid, and retries reuse the same candidate.
	token, err := h.store.PrepareWebhookToken(instanceID)
	if err != nil {
		http.Error(w, `{"error":"failed to prepare webhook credentials"}`, http.StatusInternalServerError)
		return
	}

	client := newArrConfigurationClient(inst.URL, inst.APIKey, token)
	action, err := client.upsertWebhook(r.Context(), inst.ServiceType, callbackURL, token)
	if err != nil {
		// arrConfigurationClient errors carry method/path/status plus, on a
		// validation failure, the arr's own errorMessage — extracted from the
		// narrow lineage validation shape only, never the raw body (which can
		// echo submitted credentials) — so the admin sees the arr's actual
		// verdict: "Unable to send test message: …" is a callback-reachability
		// problem, anything else names the rejected setting.
		hint := ""
		if strings.Contains(err.Error(), "status 400") {
			hint = fmt.Sprintf(" (the arr tests the webhook during configuration; the callback %s must be reachable from the arr's own network — not from browsers)", callbackURL)
		}
		log.Printf("instance: configure %s webhook for %s failed: %v (callback %s)", inst.ServiceType, instanceID, err, callbackURL)
		errorBody, encodeErr := json.Marshal(map[string]string{
			"error": fmt.Sprintf("failed to configure %s webhook: %s%s", inst.ServiceType, err, hint),
		})
		if encodeErr != nil {
			errorBody = []byte(`{"error":"failed to configure webhook"}`)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write(errorBody)
		return
	}
	if err := h.store.PromoteWebhookToken(instanceID, token); err != nil {
		// The arr may already be using this candidate, but it remains accepted as
		// pending. A retry safely reuses it and finishes promotion.
		http.Error(w, `{"error":"webhook configured but credential promotion is pending; retry"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "configured",
		"action": action,
	})
}

// Webhook status states, in the vocabulary the app renders. "stale" means a
// managed record exists but targets a different callback than the server now
// expects; "credential_missing" means the record looks right but this server
// holds no accepted callback credential (e.g. a restored database), so
// deliveries would be rejected until the webhook is reconfigured.
const (
	webhookStateOK                = "ok"
	webhookStateMissing           = "missing"
	webhookStateStale             = "stale"
	webhookStateCredentialMissing = "credential_missing"
	webhookStateNoPublicURL       = "no_public_url"
	webhookStateUnsupported       = "unsupported"
)

// WebhookStatus reports whether instant updates are actually on for this
// instance. Admins edit the arr's Connect list directly, so the answer is
// derived live from the arr on every call — never from a stored flag, which
// would keep claiming "configured" after the record was deleted or the public
// URL changed. An arr that cannot be read is a 502, not "missing": blindness
// and absence must never render the same.
func (h *Handler) WebhookStatus(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceID")
	inst, err := h.store.Get(instanceID)
	if err != nil {
		http.Error(w, `{"error":"failed to get instance"}`, http.StatusInternalServerError)
		return
	}
	if inst == nil {
		http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
		return
	}
	if !SupportsManagedWebhook(inst.ServiceType) {
		writeWebhookStatus(w, false, webhookStateUnsupported)
		return
	}

	callbackURL, err := h.arrWebhookCallbackURL(r, instanceID)
	if err != nil {
		writeWebhookStatus(w, false, webhookStateNoPublicURL)
		return
	}

	client := newArrConfigurationClient(inst.URL, inst.APIKey)
	base := "/api/" + arrNotificationAPIVersion(inst.ServiceType) + "/notification"
	var existing []map[string]any
	if err := client.doJSON(r.Context(), http.MethodGet, base, nil, &existing); err != nil {
		log.Printf("instance: webhook status for %s failed: %v", instanceID, err)
		errorBody, encodeErr := json.Marshal(map[string]string{
			"error": fmt.Sprintf("could not read the %s notification settings: %s", inst.ServiceType, err),
		})
		if encodeErr != nil {
			errorBody = []byte(`{"error":"could not read the notification settings"}`)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write(errorBody)
		return
	}

	current := findManagedWebhook(existing, callbackURL)
	if current == nil {
		writeWebhookStatus(w, false, webhookStateMissing)
		return
	}
	if configuredURL, ok := webhookResourceFieldString(current, "url"); !ok || configuredURL != callbackURL {
		writeWebhookStatus(w, false, webhookStateStale)
		return
	}
	// The arr never returns the password it holds in a comparable way, but a
	// server with no accepted credential at all would reject every delivery.
	tokens, err := h.store.WebhookTokens(instanceID)
	if err != nil {
		http.Error(w, `{"error":"failed to read webhook credentials"}`, http.StatusInternalServerError)
		return
	}
	if len(tokens) == 0 {
		writeWebhookStatus(w, false, webhookStateCredentialMissing)
		return
	}
	writeWebhookStatus(w, true, webhookStateOK)
}

func writeWebhookStatus(w http.ResponseWriter, configured bool, state string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"supported":  state != webhookStateUnsupported,
		"configured": configured,
		"state":      state,
	})
}

func (h *Handler) arrWebhookCallbackURL(r *http.Request, instanceID string) (string, error) {
	if h.publicURL != "" {
		base, err := url.Parse(h.publicURL)
		if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" ||
			base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
			return "", fmt.Errorf("invalid configured public URL")
		}
		base.Path = "/api/webhooks/arr/" + url.PathEscape(instanceID)
		return base.String(), nil
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	scheme = strings.ToLower(scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("unsupported scheme")
	}

	host := r.Host
	if !validForwardedHost(host) {
		return "", fmt.Errorf("invalid host")
	}

	callback := &url.URL{
		Scheme: scheme,
		Host:   host,
		Path:   "/api/webhooks/arr/" + url.PathEscape(instanceID),
	}
	return callback.String(), nil
}

func validForwardedHost(host string) bool {
	if host == "" || strings.ContainsAny(host, `/\\?#@`) {
		return false
	}
	for _, r := range host {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	parsed, err := url.Parse("http://" + host)
	return err == nil && parsed.Host == host && parsed.Hostname() != ""
}

type arrConfigurationClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	// redact holds the secrets this client's requests carry (instance API key,
	// candidate webhook credential); any error detail extracted from a response
	// has every occurrence replaced before it can reach a log or an admin.
	redact []string
}

func newArrConfigurationClient(baseURL, apiKey string, redact ...string) *arrConfigurationClient {
	secrets := make([]string, 0, len(redact)+1)
	if apiKey != "" {
		secrets = append(secrets, apiKey)
	}
	for _, s := range redact {
		if s != "" {
			secrets = append(secrets, s)
		}
	}
	return &arrConfigurationClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		redact:  secrets,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
			// Never carry an instance API key to a redirect target.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// SupportsManagedWebhook reports whether Cantinarr can install and receive its
// own Connect webhook for this service type. It is the single gate shared by the
// configuration endpoint and the receiver, so the two can never disagree about
// which instances may call back.
func SupportsManagedWebhook(serviceType string) bool {
	switch serviceType {
	case "radarr", "sonarr", "chaptarr":
		return true
	default:
		return false
	}
}

// arrNotificationAPIVersion is the arr's Connect/notification API version.
// Chaptarr follows the Readarr lineage at v1; Radarr and Sonarr are v3.
func arrNotificationAPIVersion(serviceType string) string {
	if serviceType == "chaptarr" {
		return "v1"
	}
	return "v3"
}

func (c *arrConfigurationClient) upsertWebhook(ctx context.Context, serviceType, callbackURL, token string) (string, error) {
	base := "/api/" + arrNotificationAPIVersion(serviceType) + "/notification"

	var schemas []map[string]any
	if err := c.doJSON(ctx, http.MethodGet, base+"/schema", nil, &schemas); err != nil {
		return "", err
	}
	template := findWebhookResource(schemas, "")
	if template == nil {
		return "", fmt.Errorf("GET %s/schema returned no Webhook provider", base)
	}

	var existing []map[string]any
	if err := c.doJSON(ctx, http.MethodGet, base, nil, &existing); err != nil {
		return "", err
	}
	current := findManagedWebhook(existing, callbackURL)
	// Fail before touching the arr: a resource we could not configure must never
	// be written, so the current credential keeps working and the admin sees why.
	if err := configureWebhookResource(template, serviceType, callbackURL, token); err != nil {
		return "", err
	}

	if current == nil {
		// The callback is constructed by the Cantinarr server rather than accepted
		// as an arbitrary URL from the app. A private/cluster-local origin is
		// therefore an intentional, trusted destination. Servarr lineage APIs
		// report those destinations as warnings and require forceSave to acknowledge
		// them; real validation errors and the provider's callback test still fail.
		if err := c.doJSON(ctx, http.MethodPost, base+"?forceSave=true", template, nil); err != nil {
			return "", err
		}
		return "created", nil
	}
	id, err := notificationResourceID(current, base)
	if err != nil {
		return "", err
	}
	template["id"] = id
	resourcePath := base + "/" + strconv.FormatInt(id, 10)
	// A forced update skips the lineage controller's built-in provider test.
	// Test the exact updated resource explicitly first so forceSave acknowledges
	// warning-severity private origins without turning reachability failures into
	// apparent success.
	if err := c.doJSON(ctx, http.MethodPost, base+"/test?forceTest=true", template, nil); err != nil {
		return "", err
	}
	if err := c.doJSON(ctx, http.MethodPut, resourcePath+"?forceSave=true", template, nil); err != nil {
		return "", err
	}
	return "updated", nil
}

// findManagedWebhook first recognizes the stable managed name, then adopts a
// webhook that already targets this exact callback path. The latter migrates
// records admins created from the old copy/paste URL (whose token lived in the
// query string) instead of leaving a duplicate Connect entry behind.
func findManagedWebhook(resources []map[string]any, callbackURL string) map[string]any {
	if named := findWebhookResource(resources, managedWebhookName); named != nil {
		return named
	}
	want, err := url.Parse(callbackURL)
	if err != nil {
		return nil
	}
	for _, resource := range resources {
		if findWebhookResource([]map[string]any{resource}, "") == nil {
			continue
		}
		configuredURL, ok := webhookResourceFieldString(resource, "url")
		if !ok {
			continue
		}
		configured, err := url.Parse(configuredURL)
		if err == nil && configured.Path == want.Path {
			return resource
		}
	}
	return nil
}

func webhookResourceFieldString(resource map[string]any, name string) (string, bool) {
	fields, _ := resource["fields"].([]any)
	for _, rawField := range fields {
		field, ok := rawField.(map[string]any)
		if !ok {
			continue
		}
		fieldName, _ := field["name"].(string)
		if !strings.EqualFold(fieldName, name) {
			continue
		}
		value, ok := field["value"].(string)
		return value, ok
	}
	return "", false
}

func findWebhookResource(resources []map[string]any, name string) map[string]any {
	for _, resource := range resources {
		implementation, _ := resource["implementation"].(string)
		configContract, _ := resource["configContract"].(string)
		if !strings.EqualFold(implementation, "Webhook") && !strings.EqualFold(configContract, "WebhookSettings") {
			continue
		}
		if name == "" {
			return resource
		}
		resourceName, _ := resource["name"].(string)
		if resourceName == name {
			return resource
		}
	}
	return nil
}

// chaptarrImportToggles are the event flags that make Chaptarr announce a
// completed book import. Verified against its open source: the notification
// resource exposes exactly onReleaseImport (no onDownload field exists in the
// fork), so the old onDownload hedge is gone. Kept as a list so a future
// spelling can be added without reshaping the require-one logic.
var chaptarrImportToggles = []string{"onReleaseImport"}

// chaptarrOptionalToggles are best-effort: enabling them keeps the app's view
// fresh when a library changes out-of-band, but none is required to alert.
// onAuthorAdded is the one with a job beyond freshness: it fires at the exact
// moment a queued author import lands, and the receiver uses it to resume the
// author-import park sweep so a waiting book request completes in seconds
// instead of at the next five-minute tick.
// onUpgrade is not cosmetic — the Readarr lineage sends no import callback for
// a book that replaces an existing file unless it is set, and that callback is
// what invalidates availability caches and feeds the admin content_upgraded
// alert. Deciding who (if anyone) is paged for an upgrade is the notifier's
// job, never this webhook's.
var chaptarrOptionalToggles = []string{"onGrab", "onUpgrade", "onAuthorAdded", "onBookAdded", "onBookDelete", "onAuthorDelete", "onBookFileDelete"}

func configureWebhookResource(resource map[string]any, serviceType, callbackURL, token string) error {
	resource["name"] = managedWebhookName
	if resource["tags"] == nil {
		resource["tags"] = []any{}
	}
	setWebhookField(resource, "url", callbackURL)
	setWebhookField(resource, "method", 1) // WebhookMethod.POST across the arr lineage.
	setWebhookField(resource, "username", managedWebhookUsername)
	setWebhookField(resource, "password", token)

	if serviceType == "chaptarr" {
		// Chaptarr's event vocabulary is verified against its open source,
		// but its schema template stays the authority at configure time:
		// write only what that schema declares and leave its settings
		// fields (which have no headers entry) untouched.
		return configureChaptarrWebhookEvents(resource)
	}

	resource["onGrab"] = true
	resource["onDownload"] = true
	resource["onUpgrade"] = true
	if serviceType == "radarr" {
		resource["onMovieAdded"] = true
		resource["onMovieDelete"] = true
		resource["onMovieFileDelete"] = true
		resource["onMovieFileDeleteForUpgrade"] = false
	} else {
		resource["onSeriesAdd"] = true
		resource["onSeriesDelete"] = true
		resource["onEpisodeFileDelete"] = true
		resource["onEpisodeFileDeleteForUpgrade"] = false
	}
	setWebhookField(resource, "headers", []any{})
	return nil
}

// configureChaptarrWebhookEvents enables the event flags Chaptarr's own schema
// template advertises. Enabling a flag the fork does not model would either be
// dropped silently or rejected outright, so nothing is invented here.
//
// It fails when no import flag is available rather than saving a webhook that
// would never fire: a loud error at configure time is recoverable, whereas a
// webhook that saves cleanly and stays silent looks like working instant
// updates until someone notices books arriving without an alert.
func configureChaptarrWebhookEvents(resource map[string]any) error {
	applied := false
	for _, name := range chaptarrImportToggles {
		if setSupportedWebhookEvent(resource, name, true) {
			applied = true
		}
	}
	if !applied {
		return fmt.Errorf("the notification schema exposes no supported book-import event toggle (looked for %s)",
			strings.Join(chaptarrImportToggles, ", "))
	}
	for _, name := range chaptarrOptionalToggles {
		setSupportedWebhookEvent(resource, name, true)
	}
	setSupportedWebhookEvent(resource, "onBookFileDeleteForUpgrade", false)
	return nil
}

// setSupportedWebhookEvent writes an on* flag only when the schema declares it
// and does not report it unsupported, and says whether it did. An absent
// supportsOn* key means "unknown", which is treated as allowed so a trimmed
// serializer does not disable every event.
func setSupportedWebhookEvent(resource map[string]any, name string, value bool) bool {
	if _, declared := resource[name]; !declared {
		return false
	}
	if supported, ok := resource["supports"+strings.ToUpper(name[:1])+name[1:]].(bool); ok && !supported {
		return false
	}
	resource[name] = value
	return true
}

func setWebhookField(resource map[string]any, name string, value any) {
	fields, _ := resource["fields"].([]any)
	for _, rawField := range fields {
		field, ok := rawField.(map[string]any)
		if !ok {
			continue
		}
		fieldName, _ := field["name"].(string)
		if strings.EqualFold(fieldName, name) {
			field["value"] = value
			return
		}
	}
	resource["fields"] = append(fields, map[string]any{"name": name, "value": value})
}

func notificationResourceID(resource map[string]any, listPath string) (int64, error) {
	switch id := resource["id"].(type) {
	case json.Number:
		if parsed, err := id.Int64(); err == nil && parsed > 0 {
			return parsed, nil
		}
	case float64:
		if id > 0 && id == float64(int64(id)) {
			return int64(id), nil
		}
	case int64:
		if id > 0 {
			return id, nil
		}
	case int:
		if id > 0 {
			return int64(id), nil
		}
	}
	return 0, fmt.Errorf("GET %s returned an invalid managed Webhook id", listPath)
}

// arrValidationDetailMaxLen bounds how much extracted error text may travel
// into an error string — enough for a few validation failures, never a body.
const arrValidationDetailMaxLen = 300

// arrValidationDetail extracts the arr's own verdict from an error response.
// Only the narrow, well-known lineage shapes are read: the validation-failure
// array's propertyName + errorMessage, or a top-level message/error string.
// Arbitrary body content is never reflected — in particular attemptedValue,
// which echoes submitted field values (the webhook credential among them), is
// deliberately ignored. An unrecognized body yields "".
func arrValidationDetail(body []byte) string {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()

	var failures []map[string]any
	if err := decoder.Decode(&failures); err == nil {
		var parts []string
		for _, failure := range failures {
			message, _ := failure["errorMessage"].(string)
			if message == "" {
				continue
			}
			if property, _ := failure["propertyName"].(string); property != "" {
				message = property + ": " + message
			}
			parts = append(parts, message)
		}
		return strings.Join(parts, "; ")
	}

	var envelope map[string]any
	decoder = json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err == nil {
		if message, _ := envelope["message"].(string); message != "" {
			return message
		}
		if message, _ := envelope["error"].(string); message != "" {
			return message
		}
	}
	return ""
}

// sanitizeDetail makes extracted error text safe to carry: secrets this
// client's requests held are redacted, control characters stripped, whitespace
// collapsed, and the whole thing bounded.
func (c *arrConfigurationClient) sanitizeDetail(detail string) string {
	for _, secret := range c.redact {
		detail = strings.ReplaceAll(detail, secret, "[redacted]")
	}
	detail = strings.Join(strings.Fields(detail), " ")
	cleaned := make([]rune, 0, len(detail))
	for _, r := range detail {
		if unicode.IsControl(r) {
			continue
		}
		cleaned = append(cleaned, r)
	}
	if len(cleaned) > arrValidationDetailMaxLen {
		cleaned = append(cleaned[:arrValidationDetailMaxLen], '…')
	}
	return string(cleaned)
}

func (c *arrConfigurationClient) doJSON(ctx context.Context, method, requestPath string, body, out any) error {
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s %s request", method, requestPath)
		}
		requestBody = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+requestPath, requestBody)
	if err != nil {
		return fmt.Errorf("build %s %s request", method, requestPath)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s failed", method, requestPath)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		encoded, _ := io.ReadAll(io.LimitReader(resp.Body, maxArrConfigurationBytes))
		if detail := c.sanitizeDetail(arrValidationDetail(encoded)); detail != "" {
			return fmt.Errorf("%s %s returned status %d: %s", method, requestPath, resp.StatusCode, detail)
		}
		return fmt.Errorf("%s %s returned status %d", method, requestPath, resp.StatusCode)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxArrConfigurationBytes))
		return nil
	}
	encoded, err := io.ReadAll(io.LimitReader(resp.Body, maxArrConfigurationBytes+1))
	if err != nil {
		return fmt.Errorf("read %s %s response", method, requestPath)
	}
	if len(encoded) > maxArrConfigurationBytes {
		return fmt.Errorf("%s %s response is too large", method, requestPath)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode %s %s response", method, requestPath)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("decode %s %s response", method, requestPath)
	}
	return nil
}
