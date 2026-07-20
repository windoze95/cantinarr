package mcp

// The arr settings tools: instance discovery, quality-profile/custom-format
// inspection, and custom-format upserts. Settings objects are fetched verbatim
// (json.RawMessage) so full-object writes can merge onto the service's live
// object without dropping unknown fields; summary views keep full profile JSON
// out of model context unless one object is explicitly requested.
//
// The full views embed that raw JSON after a prose header. That is safe for
// quality profiles and custom formats specifically: neither carries a
// credential, and a custom format's fields[] entries are service-defined
// (value/min/max), never secrets. Do NOT extend the header+raw pattern to
// indexer, download client, notification, or import list settings — those
// carry {"name":"apiKey","value":...} pairs, and prefixing prose defeats
// secrets.RedactText's structural decode, which is what redacts that shape.
// Read those endpoints through a structurally redacted view instead.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

var arrSettingsServices = []string{"radarr", "sonarr", "chaptarr"}

const maxLanguageCatalogOutputBytes = 20 << 10

var (
	errSettingsToolDisabled  = errors.New("the settings write tool was disabled before the write")
	errSettingsTargetChanged = errors.New("the selected arr instance changed while the write was being prepared")
	errCustomFormatChanged   = errors.New("the custom format changed while the write was being prepared")
)

// arrSettingsToolDefinitions are appended to toolDefinitions by the init in
// arr_tools.go.
var arrSettingsToolDefinitions = []Tool{
	{
		Name:        "list_arr_instances",
		Permission:  auth.PermissionInstancesManage,
		Description: "List the configured Radarr/Sonarr/Chaptarr instances with the instance_id values other settings tools accept, and which instance is each service's default. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"service": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"radarr", "sonarr", "chaptarr"},
					"description": "Limit the list to one service (default: all)",
				},
			},
		},
	},
	{
		Name:        "get_quality_profiles",
		Permission:  auth.PermissionInstancesManage,
		Description: "Read the quality profiles of a Radarr/Sonarr/Chaptarr instance. Without profile_id: a summary of every profile (allowed qualities, cutoff, upgrade policy, custom-format scores, language); include_languages adds the complete bounded live Radarr/Sonarr language catalog, while language_name looks up one exact live name/ID. With profile_id: that one profile's full JSON exactly as the service stores it. Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"service": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"radarr", "sonarr", "chaptarr"},
					"description": "Which service's profiles to read",
				},
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Instance ID from list_arr_instances (default: the service's default instance)",
				},
				"profile_id": map[string]interface{}{
					"type":        "integer",
					"description": "Return this one profile's full stored JSON instead of the summary list",
				},
				"include_languages": map[string]interface{}{
					"type":        "boolean",
					"description": "With the summary view, include the live Radarr/Sonarr release-language catalog and IDs used by LanguageSpecification custom formats; IDs may vary by service/version",
				},
				"language_name": map[string]interface{}{
					"type":        "string",
					"minLength":   1,
					"maxLength":   256,
					"description": "With the summary view, look up one exact Radarr/Sonarr release-language name and its live ID; IDs may vary by service/version",
				},
			},
			"required": []string{"service"},
		},
	},
	{
		Name:        "get_custom_formats",
		Permission:  auth.PermissionInstancesManage,
		Description: "Read the custom formats of a Radarr/Sonarr/Chaptarr instance. Without format_id: a summary of every custom format and its specifications. With format_id: that one format's full JSON exactly as the service stores it. The scores that make formats matter live in each quality profile (see get_quality_profiles). Admin only",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"service": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"radarr", "sonarr", "chaptarr"},
					"description": "Which service's custom formats to read",
				},
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Instance ID from list_arr_instances (default: the service's default instance)",
				},
				"format_id": map[string]interface{}{
					"type":        "integer",
					"description": "Return this one custom format's full stored JSON instead of the summary list",
				},
			},
			"required": []string{"service"},
		},
	},
	{
		Name:        "upsert_custom_format",
		Permission:  auth.PermissionInstancesManage,
		Description: "Create or update one Radarr/Sonarr/Chaptarr custom format by exact name from native or TRaSH-style JSON. Caller-supplied ids are ignored. A create enters every existing quality profile at score 0; an update preserves the profile's numeric score but does not recompute stored file matches. A successful write is read back and recorded in Configuration history for live comparison. This tool does not set profile scores. Admin only",
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"service": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"radarr", "sonarr", "chaptarr"},
					"description": "Which service owns the custom format",
				},
				"instance_id": map[string]interface{}{
					"type":        "string",
					"description": "Instance ID from list_arr_instances (default: the service's default instance)",
				},
				"custom_format": map[string]interface{}{
					"type":        "object",
					"description": "One native arr or TRaSH custom-format object. Identity is its name; specifications[].fields may be the native array or TRaSH object form.",
					"properties": map[string]interface{}{
						"name": map[string]interface{}{
							"type":      "string",
							"minLength": 1,
							"maxLength": 256,
						},
						"specifications": map[string]interface{}{
							"type":     "array",
							"maxItems": 256,
						},
					},
					"required": []string{"name", "specifications"},
				},
			},
			"required": []string{"service", "custom_format"},
		},
	},
}

// settingsReader is the read surface the settings tools need from an arr
// client. All three arr clients satisfy it.
type settingsReader interface {
	GetQualityProfilesRaw() ([]json.RawMessage, error)
	GetCustomFormatsRaw() ([]json.RawMessage, error)
}

func arrServiceLabel(service string) string {
	switch service {
	case "radarr":
		return "Radarr"
	case "sonarr":
		return "Sonarr"
	case "chaptarr":
		return "Chaptarr"
	}
	return service
}

// settingsTargetFor resolves the client, stable instance ID, and display label
// a settings tool targets. A non-empty refusal is a complete user-facing
// answer; it keeps "no instance with that ID" distinct from "service not
// configured" so a mistyped instance_id never reads as an unconfigured service.
func (s *ToolServer) settingsTargetFor(service, instanceID string) (settingsReader, string, string, string) {
	label := arrServiceLabel(service)
	if !slices.Contains(arrSettingsServices, service) {
		return nil, "", "", "Unknown service — expected radarr, sonarr, or chaptarr."
	}
	if s.registry == nil {
		return nil, "", "", label + " is not configured."
	}
	var (
		reader     settingsReader
		resolvedID string
	)
	switch service {
	case "radarr":
		if instanceID != "" {
			client, err := s.registry.GetRadarrClient(instanceID)
			if err != nil {
				return nil, "", "", s.instanceResolveFailureText(service, instanceID)
			}
			reader, resolvedID = client, instanceID
		} else {
			client, id, err := s.registry.GetDefaultRadarrClient()
			if err != nil || client == nil {
				return nil, "", "", label + " is not configured."
			}
			reader, resolvedID = client, id
		}
	case "sonarr":
		if instanceID != "" {
			client, err := s.registry.GetSonarrClient(instanceID)
			if err != nil {
				return nil, "", "", s.instanceResolveFailureText(service, instanceID)
			}
			reader, resolvedID = client, instanceID
		} else {
			client, id, err := s.registry.GetDefaultSonarrClient()
			if err != nil || client == nil {
				return nil, "", "", label + " is not configured."
			}
			reader, resolvedID = client, id
		}
	case "chaptarr":
		if instanceID != "" {
			client, err := s.registry.GetChaptarrClient(instanceID)
			if err != nil {
				return nil, "", "", s.instanceResolveFailureText(service, instanceID)
			}
			reader, resolvedID = client, instanceID
		} else {
			client, id, err := s.registry.GetDefaultChaptarrClient()
			if err != nil || client == nil {
				return nil, "", "", label + " is not configured."
			}
			reader, resolvedID = client, id
		}
	}
	return reader, resolvedID, s.arrInstanceLabel(service, resolvedID), ""
}

func (s *ToolServer) settingsReaderFor(service, instanceID string) (settingsReader, string, string) {
	reader, _, label, refusal := s.settingsTargetFor(service, instanceID)
	return reader, label, refusal
}

// freshSettingsTargetFor bypasses the registry client cache and binds the
// returned client to a fingerprint computed from the same authoritative store
// row. Consequential read/modify/write paths use it after their lock.
func (s *ToolServer) freshSettingsTargetFor(service, requestedID string) (settingsReader, string, string, instance.ArrSettingsFingerprint, string) {
	label := arrServiceLabel(service)
	if s.registry == nil {
		return nil, "", "", instance.ArrSettingsFingerprint{}, label + " is not configured."
	}
	resolvedID := requestedID
	if resolvedID == "" {
		var err error
		resolvedID, err = s.registry.GetDefaultInstanceID(service)
		if err != nil || resolvedID == "" {
			return nil, "", "", instance.ArrSettingsFingerprint{}, label + " is not configured."
		}
	}

	var (
		reader      settingsReader
		fingerprint instance.ArrSettingsFingerprint
		err         error
	)
	switch service {
	case "radarr":
		reader, fingerprint, err = s.registry.GetFreshRadarrClient(resolvedID)
	case "sonarr":
		reader, fingerprint, err = s.registry.GetFreshSonarrClient(resolvedID)
	case "chaptarr":
		reader, fingerprint, err = s.registry.GetFreshChaptarrClient(resolvedID)
	default:
		return nil, "", "", instance.ArrSettingsFingerprint{}, "service must be radarr, sonarr, or chaptarr."
	}
	if err != nil || reader == nil {
		return nil, "", "", instance.ArrSettingsFingerprint{}, s.instanceResolveFailureText(service, resolvedID)
	}
	return reader, resolvedID, s.arrInstanceLabel(service, resolvedID), fingerprint, ""
}

// lockArrSettingsMutation serializes full-object settings writes per resolved
// service instance. Wave 2 custom-format writes and Wave 3 profile writes share
// this lock because creating a custom format also mutates every profile.
func (s *ToolServer) lockArrSettingsMutation(ctx context.Context, service, instanceID string) (func(), error) {
	key := service + "\x00" + instanceID
	s.settingsMutationMu.Lock()
	lock := s.settingsMutationLocks[key]
	if lock == nil {
		lock = make(chan struct{}, 1)
		lock <- struct{}{}
		s.settingsMutationLocks[key] = lock
	}
	s.settingsMutationMu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-lock:
		if err := ctx.Err(); err != nil {
			lock <- struct{}{}
			return nil, err
		}
		return func() { lock <- struct{}{} }, nil
	}
}

// instanceResolveFailureText separates a mistyped or wrong-service
// instance_id from an instance that exists but could not be opened (a store
// read failure, or stored credentials that will not decrypt). Sending an
// admin to re-check an ID that list_arr_instances just printed would hide the
// real fault, so existence is confirmed against the listing first.
func (s *ToolServer) instanceResolveFailureText(service, instanceID string) string {
	summaries, err := s.registry.ListInstanceSummaries(service)
	if err == nil {
		for _, summary := range summaries {
			if summary.ID == instanceID {
				return fmt.Sprintf("The %s instance %q exists but could not be opened. Check the server logs — its stored credentials may be unreadable.", service, summary.Name)
			}
		}
	}
	return fmt.Sprintf("No %s instance with ID %q. Call list_arr_instances to see the configured instances.", service, instanceID)
}

// arrInstanceLabel names an instance for tool output without ever exposing
// its URL.
func (s *ToolServer) arrInstanceLabel(service, instanceID string) string {
	label := arrServiceLabel(service)
	if name := s.arrInstanceName(service, instanceID); name != "" {
		return fmt.Sprintf("%s instance %q", label, name)
	}
	return label
}

func (s *ToolServer) arrInstanceName(service, instanceID string) string {
	if s.registry == nil {
		return ""
	}
	summaries, err := s.registry.ListInstanceSummaries(service)
	if err == nil {
		for _, summary := range summaries {
			if summary.ID == instanceID {
				return summary.Name
			}
		}
	}
	return ""
}

// --- list_arr_instances ---

func (s *ToolServer) listArrInstances(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		Service string `json:"service"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("parse input: %w", err)
		}
	}
	services := arrSettingsServices
	if params.Service != "" {
		if !slices.Contains(arrSettingsServices, params.Service) {
			return &ToolResult{Text: "Unknown service — expected radarr, sonarr, or chaptarr."}, nil
		}
		services = []string{params.Service}
	}
	if s.registry == nil {
		return &ToolResult{Text: "No service instances are configured."}, nil
	}
	var sb strings.Builder
	for _, service := range services {
		summaries, err := s.registry.ListInstanceSummaries(service)
		if err != nil {
			return nil, err
		}
		label := arrServiceLabel(service)
		if len(summaries) == 0 {
			fmt.Fprintf(&sb, "%s: no instances configured.\n", label)
			continue
		}
		fmt.Fprintf(&sb, "%s instances:\n", label)
		// Mark what an omitted instance_id actually resolves to, not the raw
		// is_default column. Store.GetDefault falls back to the first row by
		// sort order when no row is flagged — always the case for Chaptarr,
		// which has no global default flag, and also reachable for Radarr and
		// Sonarr once a flagged default is deleted. summaries arrive in that
		// same order, so index 0 is the fallback pick.
		defaultIdx := 0
		for i, summary := range summaries {
			if summary.IsDefault {
				defaultIdx = i
				break
			}
		}
		for i, summary := range summaries {
			fmt.Fprintf(&sb, "- %q — instance_id: %s", summary.Name, summary.ID)
			if i == defaultIdx {
				sb.WriteString(" (used when no instance_id is given)")
			}
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\nSettings tools accept instance_id to target one of these; without it they use the service default.")
	return &ToolResult{Text: sb.String()}, nil
}

// --- get_quality_profiles ---

type arrIDName struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type arrProfileItemView struct {
	ID      int                  `json:"id"`
	Name    string               `json:"name"`
	Allowed *bool                `json:"allowed"`
	Quality *arrIDName           `json:"quality"`
	Items   []arrProfileItemView `json:"items"`
}

type arrFormatItemView struct {
	Format int    `json:"format"`
	Name   string `json:"name"`
	Score  int    `json:"score"`
}

type arrQualityProfileView struct {
	ID                int                  `json:"id"`
	Name              string               `json:"name"`
	UpgradeAllowed    bool                 `json:"upgradeAllowed"`
	Cutoff            int                  `json:"cutoff"`
	MinFormatScore    int                  `json:"minFormatScore"`
	CutoffFormatScore int                  `json:"cutoffFormatScore"`
	Language          *arrIDName           `json:"language"`
	Items             []arrProfileItemView `json:"items"`
	FormatItems       []arrFormatItemView  `json:"formatItems"`
}

// allowedNames lists the enabled top-level quality entries in ranking order
// (worst first, matching the arr's items order).
func (v arrQualityProfileView) allowedNames() []string {
	names := make([]string, 0, len(v.Items))
	for _, item := range v.Items {
		if item.Allowed == nil || !*item.Allowed {
			continue
		}
		switch {
		case item.Quality != nil:
			names = append(names, item.Quality.Name)
		case item.Name != "":
			names = append(names, item.Name)
		default:
			names = append(names, fmt.Sprintf("item %d", item.ID))
		}
	}
	return names
}

// cutoffName resolves the cutoff id against the top-level items (a leaf
// quality id or a group id).
func (v arrQualityProfileView) cutoffName() string {
	for _, item := range v.Items {
		if item.Quality != nil && item.Quality.ID == v.Cutoff {
			return item.Quality.Name
		}
		if item.Quality == nil && item.ID == v.Cutoff && item.Name != "" {
			return item.Name
		}
	}
	return ""
}

func (s *ToolServer) getQualityProfiles(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
	var params struct {
		Service          string `json:"service"`
		InstanceID       string `json:"instance_id"`
		ProfileID        int    `json:"profile_id"`
		IncludeLanguages bool   `json:"include_languages"`
		LanguageName     string `json:"language_name"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	reader, label, refusal := s.settingsReaderFor(params.Service, params.InstanceID)
	if refusal != "" {
		return &ToolResult{Text: refusal}, nil
	}
	raws, err := reader.GetQualityProfilesRaw()
	if err != nil {
		return nil, err
	}
	if params.ProfileID != 0 {
		if params.IncludeLanguages || params.LanguageName != "" {
			return nil, fmt.Errorf("include_languages and language_name are available only with the summary view (omit profile_id)")
		}
		if raw, head, ok := findRawByID(raws, params.ProfileID); ok {
			text := fmt.Sprintf("Quality profile %d (%q) on %s — full stored JSON:\n%s", head.ID, head.Name, label, string(raw))
			return &ToolResult{Text: text}, nil
		}
		return &ToolResult{Text: fmt.Sprintf("No quality profile with ID %d on %s. Available: %s.", params.ProfileID, label, rawIDNameList(raws))}, nil
	}
	if len(raws) == 0 {
		return &ToolResult{Text: "No quality profiles exist on " + label + "."}, nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Quality profiles on %s (%d):\n", label, len(raws))
	for _, raw := range raws {
		var view arrQualityProfileView
		if err := json.Unmarshal(raw, &view); err != nil {
			// Name the id when the partial decode reached it — the prescribed
			// recovery is useless without one.
			if view.ID != 0 {
				fmt.Fprintf(&sb, "- [%d] %q — could not be summarized; fetch it by profile_id for the raw JSON\n", view.ID, view.Name)
			} else {
				sb.WriteString("- (one profile could not be decoded, and its id was unreadable)\n")
			}
			continue
		}
		renderQualityProfileSummary(&sb, view)
	}
	if params.IncludeLanguages || params.LanguageName != "" {
		if params.Service == "chaptarr" {
			sb.WriteString("\nChaptarr does not expose release-language specifications; book metadata language is configured separately.\n")
		} else if languageReader, ok := reader.(arrLanguageReader); ok {
			languages, languageErr := languageReader.GetLanguagesRawContext(ctx)
			if languageErr != nil {
				return nil, languageErr
			}
			catalog, languageErr := resolveProfileLanguageCatalog(languages)
			if languageErr != nil {
				return nil, languageErr
			}
			sort.Slice(catalog, func(i, j int) bool { return catalog[i].ID < catalog[j].ID })
			if params.LanguageName != "" {
				if strings.TrimSpace(params.LanguageName) == "" || len(params.LanguageName) > maxCustomFormatNameBytes {
					return nil, fmt.Errorf("language_name must be a nonblank exact name of at most 256 bytes")
				}
				for _, language := range catalog {
					if language.Name == params.LanguageName {
						fmt.Fprintf(&sb, "\nLive release language for this instance: %s [%d]. IDs may vary by service version; use this live result instead of reusing an ID from another service or instance.\n", language.Name, language.ID)
						return &ToolResult{Text: sb.String()}, nil
					}
				}
				return nil, fmt.Errorf("no live release language is named exactly %q on this instance", params.LanguageName)
			}
			values := make([]string, 0, len(catalog))
			for _, language := range catalog {
				values = append(values, fmt.Sprintf("%s [%d]", language.Name, language.ID))
			}
			rendered := strings.Join(values, ", ")
			if len(rendered) > maxLanguageCatalogOutputBytes {
				return nil, fmt.Errorf("the complete language catalog exceeds the safe output limit; use language_name to look up one exact name")
			}
			fmt.Fprintf(&sb, "\nLive release-language catalog for this instance: %s. IDs may vary by service version; use this live catalog instead of reusing IDs from another service or instance.\n", rendered)
		}
	}
	sb.WriteString("\nPass profile_id for one profile's full stored JSON.")
	return &ToolResult{Text: sb.String()}, nil
}

func renderQualityProfileSummary(sb *strings.Builder, view arrQualityProfileView) {
	fmt.Fprintf(sb, "- [%d] %q — upgrades %s", view.ID, view.Name, onOff(view.UpgradeAllowed))
	if cutoff := view.cutoffName(); cutoff != "" && view.UpgradeAllowed {
		fmt.Fprintf(sb, " until %s", cutoff)
	}
	if allowed := view.allowedNames(); len(allowed) > 0 {
		fmt.Fprintf(sb, "; allowed (worst→best): %s", joinCappedTail(allowed, 8))
	}
	sb.WriteString("\n")
	if len(view.FormatItems) == 0 {
		sb.WriteString("  custom formats: none\n")
	} else {
		scored := make([]string, 0, len(view.FormatItems))
		for _, item := range view.FormatItems {
			if item.Score != 0 {
				scored = append(scored, fmt.Sprintf("%s (%+d)", item.Name, item.Score))
			}
		}
		fmt.Fprintf(sb, "  custom format scores: %d of %d nonzero", len(scored), len(view.FormatItems))
		if len(scored) > 0 {
			fmt.Fprintf(sb, " — %s", joinCapped(scored, 6))
		}
		fmt.Fprintf(sb, "; min score %d, cutoff score %d\n", view.MinFormatScore, view.CutoffFormatScore)
	}
	if view.Language != nil {
		fmt.Fprintf(sb, "  language: %s\n", view.Language.Name)
	}
}

// --- get_custom_formats ---

type arrCustomFormatView struct {
	ID                              int    `json:"id"`
	Name                            string `json:"name"`
	IncludeCustomFormatWhenRenaming bool   `json:"includeCustomFormatWhenRenaming"`
	Specifications                  []struct {
		Name           string `json:"name"`
		Implementation string `json:"implementation"`
		Negate         bool   `json:"negate"`
		Required       bool   `json:"required"`
	} `json:"specifications"`
}

func (s *ToolServer) getCustomFormats(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		Service    string `json:"service"`
		InstanceID string `json:"instance_id"`
		FormatID   int    `json:"format_id"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	reader, label, refusal := s.settingsReaderFor(params.Service, params.InstanceID)
	if refusal != "" {
		return &ToolResult{Text: refusal}, nil
	}
	raws, err := reader.GetCustomFormatsRaw()
	if err != nil {
		// A 404 cannot distinguish an older build from an instance URL that
		// is missing the service's URL base, so report both causes instead of
		// diagnosing one — asserting "unsupported" would bury a fixable
		// misconfiguration behind a version claim.
		if isCustomFormatsNotFound(err) {
			return &ToolResult{Text: customFormatsUnavailableText(params.Service, label)}, nil
		}
		return nil, err
	}
	if params.FormatID != 0 {
		if raw, head, ok := findRawByID(raws, params.FormatID); ok {
			text := fmt.Sprintf("Custom format %d (%q) on %s — full stored JSON:\n%s", head.ID, head.Name, label, string(raw))
			return &ToolResult{Text: text}, nil
		}
		return &ToolResult{Text: fmt.Sprintf("No custom format with ID %d on %s. Available: %s.", params.FormatID, label, rawIDNameList(raws))}, nil
	}
	if len(raws) == 0 {
		return &ToolResult{Text: "No custom formats are defined on " + label + "."}, nil
	}
	var sb strings.Builder
	// Above this many formats the summary drops per-format specification
	// detail: a full TRaSH-guides set runs to a couple of hundred formats and
	// would render larger than the single-object full-JSON view this summary
	// exists to avoid. Ids and names always survive, because quality profile
	// scores reference formats by name and must stay resolvable.
	const maxDetailedCustomFormats = 40
	detailed := len(raws) <= maxDetailedCustomFormats
	fmt.Fprintf(&sb, "Custom formats on %s (%d):\n", label, len(raws))
	for _, raw := range raws {
		var view arrCustomFormatView
		if err := json.Unmarshal(raw, &view); err != nil {
			if view.ID != 0 {
				fmt.Fprintf(&sb, "- [%d] %q — could not be summarized; fetch it by format_id for the raw JSON\n", view.ID, view.Name)
			} else {
				sb.WriteString("- (one custom format could not be decoded, and its id was unreadable)\n")
			}
			continue
		}
		if !detailed {
			fmt.Fprintf(&sb, "- [%d] %q (%d specs)\n", view.ID, view.Name, len(view.Specifications))
			continue
		}
		specs := make([]string, 0, len(view.Specifications))
		for _, spec := range view.Specifications {
			flags := ""
			if spec.Negate {
				flags += ", negate"
			}
			if spec.Required {
				flags += ", required"
			}
			specs = append(specs, fmt.Sprintf("%s (%s%s)", spec.Name, spec.Implementation, flags))
		}
		fmt.Fprintf(&sb, "- [%d] %q — specs: %s", view.ID, view.Name, joinCapped(specs, 4))
		if view.IncludeCustomFormatWhenRenaming {
			sb.WriteString("; used in renaming")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\nScores that make these formats matter live in each quality profile's formatItems (see get_quality_profiles). Pass format_id for one format's full stored JSON.")
	return &ToolResult{Text: sb.String()}, nil
}

// --- upsert_custom_format ---

type upsertCustomFormatParams struct {
	Service      string          `json:"service"`
	InstanceID   string          `json:"instance_id"`
	CustomFormat json.RawMessage `json:"custom_format"`
}

func (s *ToolServer) upsertCustomFormat(ctx context.Context, input json.RawMessage, callCtx CallContext) (*ToolResult, error) {
	var params upsertCustomFormatParams
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("parse input: trailing JSON value")
	}

	_, resolvedID, _, refusal := s.settingsTargetFor(params.Service, params.InstanceID)
	if refusal != "" {
		return &ToolResult{Text: refusal}, nil
	}

	// Re-resolve after acquiring the lock: an omitted default may change while
	// queued, and an explicit instance may be deleted or reconfigured. If the
	// effective default moved, release the old instance lock and acquire the new
	// one before touching either service.
	var (
		mutator CustomFormatMutator
		label   string
		unlock  func()
		err     error
		binding instance.ArrSettingsFingerprint
	)
	for attempts := 0; attempts < 3; attempts++ {
		unlock, err = s.lockArrSettingsMutation(ctx, params.Service, resolvedID)
		if err != nil {
			return nil, err
		}
		callCtx, err = s.authorizeCall(ctx, callCtx)
		if err != nil {
			unlock()
			return nil, err
		}
		if !s.IsToolEnabled("upsert_custom_format") {
			unlock()
			return &ToolResult{Text: "This tool is disabled by the administrator."}, nil
		}
		if !auth.HasPermission(callCtx.Role, auth.PermissionInstancesManage) {
			unlock()
			return nil, ErrToolAuthorization
		}

		reader, freshID, freshLabel, freshBinding, freshRefusal := s.freshSettingsTargetFor(params.Service, params.InstanceID)
		if freshRefusal != "" {
			unlock()
			return &ToolResult{Text: freshRefusal}, nil
		}
		if freshID != resolvedID {
			unlock()
			resolvedID = freshID
			continue
		}
		var ok bool
		mutator, ok = reader.(CustomFormatMutator)
		if !ok {
			unlock()
			return &ToolResult{Text: arrServiceLabel(params.Service) + " custom-format writes are not available on this server build."}, nil
		}
		label = freshLabel
		binding = freshBinding
		break
	}
	if unlock == nil || mutator == nil {
		return &ToolResult{Text: "The default instance changed repeatedly while this write was queued. Retry with an explicit instance_id."}, nil
	}
	defer unlock()
	instanceName := s.arrInstanceName(params.Service, resolvedID)
	if instanceName == "" {
		return &ToolResult{Text: "The selected arr instance no longer has a readable identity. No custom format was changed."}, nil
	}
	var historyChange storedSettingChange

	beforeWrite := func(ctx context.Context, planned customFormatUpsertPlan) error {
		var guardErr error
		callCtx, guardErr = s.authorizeCall(ctx, callCtx)
		if guardErr != nil {
			return guardErr
		}
		if !auth.HasPermission(callCtx.Role, auth.PermissionInstancesManage) {
			return ErrToolAuthorization
		}
		if !s.IsToolEnabled("upsert_custom_format") {
			return errSettingsToolDisabled
		}
		freshReader, freshID, _, freshBinding, freshRefusal := s.freshSettingsTargetFor(params.Service, params.InstanceID)
		if freshRefusal != "" || freshID != resolvedID || freshBinding != binding {
			return errSettingsTargetChanged
		}
		freshMutator, ok := freshReader.(CustomFormatMutator)
		if !ok {
			return errSettingsTargetChanged
		}
		currentFormats, guardErr := freshMutator.GetCustomFormatsRawContext(ctx)
		if guardErr != nil {
			return guardErr
		}
		latest, guardErr := buildCustomFormatUpsertPlan(currentFormats, params.CustomFormat)
		if guardErr != nil {
			return guardErr
		}
		if latest.Action != planned.Action || latest.ID != planned.ID || latest.Name != planned.Name ||
			latest.BeforeHash != planned.BeforeHash || latest.AfterHash != planned.AfterHash {
			return errCustomFormatChanged
		}
		fields, guardErr := customFormatSettingFieldChanges(latest)
		if guardErr != nil {
			return guardErr
		}
		resourceID := "name:" + latest.Name
		operation := "create"
		summary := settingChangeSummary("custom_format", "create", latest.Name)
		if latest.ID > 0 {
			resourceID = strconv.Itoa(latest.ID)
			operation = "update"
			summary = settingChangeSummary("custom_format", "update", latest.Name)
		}
		source := "external_mcp"
		if callCtx.TrustedInternal {
			source = "system"
		} else if callCtx.Origin == OriginInteractiveChat {
			source = "ai_chat"
		}
		historyChange, guardErr = s.settingsChanges.create(newSettingChange{
			ActorUserID: callCtx.UserID, ActorDeviceID: callCtx.DeviceID,
			Source: source, ServiceType: params.Service,
			InstanceID: resolvedID, InstanceName: instanceName,
			ResourceType: "custom_format", ResourceID: resourceID,
			ResourceName: latest.Name, Operation: operation, Summary: summary,
			Changes: fields, BeforeRaw: latest.BeforeRaw, AfterRaw: latest.AfterRaw,
			BeforeHash: latest.BeforeHash, AfterHash: latest.AfterHash,
			InstanceBinding: binding,
		})
		return guardErr
	}

	result, err := UpsertCustomFormatHelper(ctx, mutator, params.CustomFormat, beforeWrite)
	if err != nil {
		var partial *PartialMutationError
		if historyChange.ID != 0 {
			status := settingChangeStatusFailed
			if errors.As(err, &partial) {
				status = settingChangeStatusOutcomeUnknown
			}
			_, _ = s.settingsChanges.finish(historyChange.ID, status, secrets.RedactText(err.Error()))
		}
		if errors.As(err, &partial) {
			return nil, err
		}
		if errors.Is(err, errSettingsToolDisabled) {
			return &ToolResult{Text: "This tool was disabled by the administrator before the write. No custom format was changed."}, nil
		}
		if errors.Is(err, errSettingsTargetChanged) {
			return &ToolResult{Text: "The selected arr instance changed while this write was being prepared. No custom format was changed; review the current instance and try again."}, nil
		}
		if errors.Is(err, errCustomFormatChanged) {
			return &ToolResult{Text: "The custom format changed while this write was being prepared. No write was attempted; review the live settings and try again."}, nil
		}
		if errors.Is(err, radarr.ErrCustomFormatsNotFound) || errors.Is(err, sonarr.ErrCustomFormatsNotFound) || errors.Is(err, chaptarr.ErrCustomFormatsNotFound) {
			return &ToolResult{Text: customFormatsUnavailableText(params.Service, label)}, nil
		}
		return nil, err
	}
	if result.Action == "unchanged" {
		return &ToolResult{Text: fmt.Sprintf("Custom format %d (%q) on %s already matches the requested settings. Nothing was changed, so no history entry was created.", result.ID, result.Name, label)}, nil
	}
	verifiedHash, err := canonicalJSONHash(result.VerifiedRaw)
	if err != nil {
		return nil, &PartialMutationError{Completed: "the custom format write was accepted and read back", Pending: "validating its change-history snapshot", Err: err}
	}
	verifiedFields, err := customFormatSettingFieldChanges(customFormatUpsertPlan{
		Action: result.Action, BeforeRaw: historyChange.BeforeRaw, AfterRaw: result.VerifiedRaw,
	})
	if err != nil {
		_, _ = s.settingsChanges.finish(historyChange.ID, settingChangeStatusFailed, "The service accepted the write but did not retain a configuration difference.")
		return nil, &PartialMutationError{Completed: "the custom format write was accepted and read back", Pending: "confirming a stored configuration difference", Err: err}
	}
	historyChange, err = s.settingsChanges.finishAppliedVerified(historyChange.ID, strconv.Itoa(result.ID), result.Name, verifiedFields, result.VerifiedRaw, verifiedHash)
	if err != nil {
		return nil, &PartialMutationError{Completed: "the custom format write was applied and verified", Pending: "finalizing its durable change-history record", Err: err}
	}

	if result.Action == "created" {
		return &ToolResult{Text: fmt.Sprintf("Created custom format %d (%q) on %s and recorded change #%d. The service added it to every existing quality profile at score 0; this tool did not change any profile scores.", result.ID, result.Name, label, historyChange.ID), StructuredData: historyChange.ExternalSettingChange}, nil
	}
	return &ToolResult{Text: fmt.Sprintf("Updated custom format %d (%q) on %s and recorded change #%d. Profiles kept their existing numeric scores. The arr will use the new rules for future matching; this tool did not recompute stored file matches.", result.ID, result.Name, label, historyChange.ID), StructuredData: historyChange.ExternalSettingChange}, nil
}

func isCustomFormatsNotFound(err error) bool {
	return errors.Is(err, radarr.ErrCustomFormatsNotFound) ||
		errors.Is(err, sonarr.ErrCustomFormatsNotFound) ||
		errors.Is(err, chaptarr.ErrCustomFormatsNotFound)
}

func customFormatsUnavailableText(service, label string) string {
	version := "this build may predate custom formats"
	if service == "sonarr" {
		version = "Sonarr gained custom formats in v4, so a v3 instance has no such endpoint"
	}
	return fmt.Sprintf("%s returned 404 for the custom format endpoint: %s, or the stored instance URL is missing the service's URL base.", label, version)
}

// --- shared helpers ---

// findRawByID scans raw settings objects for the one whose decoded id
// matches.
func findRawByID(raws []json.RawMessage, id int) (json.RawMessage, arrIDName, bool) {
	for _, raw := range raws {
		var head arrIDName
		if json.Unmarshal(raw, &head) == nil && head.ID == id {
			return raw, head, true
		}
	}
	return nil, arrIDName{}, false
}

// rawIDNameList renders "1 (Any), 6 (HD-1080p)" so a miss teaches the caller
// the valid ids.
func rawIDNameList(raws []json.RawMessage) string {
	if len(raws) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(raws))
	for _, raw := range raws {
		var head arrIDName
		if json.Unmarshal(raw, &head) != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d (%s)", head.ID, head.Name))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func joinCapped(parts []string, limit int) string {
	if len(parts) <= limit {
		return strings.Join(parts, ", ")
	}
	return fmt.Sprintf("%s, +%d more", strings.Join(parts[:limit], ", "), len(parts)-limit)
}

// joinCappedTail keeps the LAST entries, for lists whose most significant
// items sort last. Quality items arrive worst-first, so capping from the
// front would hide exactly the ceiling a caller asks about.
func joinCappedTail(parts []string, limit int) string {
	if len(parts) <= limit {
		return strings.Join(parts, ", ")
	}
	return fmt.Sprintf("+%d more, %s", len(parts)-limit, strings.Join(parts[len(parts)-limit:], ", "))
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
