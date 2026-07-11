package instance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	if inst.ServiceType != "radarr" && inst.ServiceType != "sonarr" {
		http.Error(w, `{"error":"webhooks are supported only for radarr and sonarr"}`, http.StatusBadRequest)
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

	client := newArrConfigurationClient(inst.URL, inst.APIKey)
	action, err := client.upsertWebhook(r.Context(), inst.ServiceType, callbackURL, token)
	if err != nil {
		// arrConfigurationClient errors contain method/path/status only, never a
		// response body or the request payload carrying the callback credential.
		http.Error(w, fmt.Sprintf(`{"error":"failed to configure %s webhook: %s"}`, inst.ServiceType, err), http.StatusBadGateway)
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
}

func newArrConfigurationClient(baseURL, apiKey string) *arrConfigurationClient {
	return &arrConfigurationClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
			// Never carry an instance API key to a redirect target.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *arrConfigurationClient) upsertWebhook(ctx context.Context, serviceType, callbackURL, token string) (string, error) {
	var schemas []map[string]any
	if err := c.doJSON(ctx, http.MethodGet, "/api/v3/notification/schema", nil, &schemas); err != nil {
		return "", err
	}
	template := findWebhookResource(schemas, "")
	if template == nil {
		return "", fmt.Errorf("GET /api/v3/notification/schema returned no Webhook provider")
	}

	var existing []map[string]any
	if err := c.doJSON(ctx, http.MethodGet, "/api/v3/notification", nil, &existing); err != nil {
		return "", err
	}
	current := findManagedWebhook(existing, callbackURL)
	configureWebhookResource(template, serviceType, callbackURL, token)

	if current == nil {
		if err := c.doJSON(ctx, http.MethodPost, "/api/v3/notification", template, nil); err != nil {
			return "", err
		}
		return "created", nil
	}
	id, err := notificationResourceID(current)
	if err != nil {
		return "", err
	}
	template["id"] = id
	resourcePath := "/api/v3/notification/" + strconv.FormatInt(id, 10)
	if err := c.doJSON(ctx, http.MethodPut, resourcePath, template, nil); err != nil {
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

func configureWebhookResource(resource map[string]any, serviceType, callbackURL, token string) {
	resource["name"] = managedWebhookName
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
	if resource["tags"] == nil {
		resource["tags"] = []any{}
	}
	setWebhookField(resource, "url", callbackURL)
	setWebhookField(resource, "method", 1) // WebhookMethod.POST in Radarr/Sonarr.
	setWebhookField(resource, "username", managedWebhookUsername)
	setWebhookField(resource, "password", token)
	setWebhookField(resource, "headers", []any{})
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

func notificationResourceID(resource map[string]any) (int64, error) {
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
	return 0, fmt.Errorf("GET /api/v3/notification returned an invalid managed Webhook id")
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
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxArrConfigurationBytes))
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
