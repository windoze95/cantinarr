package mcp

// The arr settings read tools: instance discovery plus quality-profile and
// custom-format inspection. Settings objects are fetched verbatim
// (json.RawMessage) so a future write wave can round-trip them without
// dropping fields; the summary views here exist to keep full profile JSON out
// of model context unless one object is explicitly requested.
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
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

var arrSettingsServices = []string{"radarr", "sonarr", "chaptarr"}

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
		Description: "Read the quality profiles of a Radarr/Sonarr/Chaptarr instance. Without profile_id: a summary of every profile (allowed qualities, cutoff, upgrade policy, custom-format scores, language). With profile_id: that one profile's full JSON exactly as the service stores it. Admin only",
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

// settingsReaderFor resolves the client and display label a settings tool
// targets. A non-empty refusal is a complete user-facing answer; it keeps "no
// instance with that ID" distinct from "service not configured" so a mistyped
// instance_id never reads as an unconfigured service.
func (s *ToolServer) settingsReaderFor(service, instanceID string) (settingsReader, string, string) {
	label := arrServiceLabel(service)
	if !slices.Contains(arrSettingsServices, service) {
		return nil, "", "Unknown service — expected radarr, sonarr, or chaptarr."
	}
	if s.registry == nil {
		return nil, "", label + " is not configured."
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
				return nil, "", s.instanceResolveFailureText(service, instanceID)
			}
			reader, resolvedID = client, instanceID
		} else {
			client, id, err := s.registry.GetDefaultRadarrClient()
			if err != nil || client == nil {
				return nil, "", label + " is not configured."
			}
			reader, resolvedID = client, id
		}
	case "sonarr":
		if instanceID != "" {
			client, err := s.registry.GetSonarrClient(instanceID)
			if err != nil {
				return nil, "", s.instanceResolveFailureText(service, instanceID)
			}
			reader, resolvedID = client, instanceID
		} else {
			client, id, err := s.registry.GetDefaultSonarrClient()
			if err != nil || client == nil {
				return nil, "", label + " is not configured."
			}
			reader, resolvedID = client, id
		}
	case "chaptarr":
		if instanceID != "" {
			client, err := s.registry.GetChaptarrClient(instanceID)
			if err != nil {
				return nil, "", s.instanceResolveFailureText(service, instanceID)
			}
			reader, resolvedID = client, instanceID
		} else {
			client, id, err := s.registry.GetDefaultChaptarrClient()
			if err != nil || client == nil {
				return nil, "", label + " is not configured."
			}
			reader, resolvedID = client, id
		}
	}
	return reader, s.arrInstanceLabel(service, resolvedID), ""
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
	summaries, err := s.registry.ListInstanceSummaries(service)
	if err == nil {
		for _, summary := range summaries {
			if summary.ID == instanceID {
				return fmt.Sprintf("%s instance %q", label, summary.Name)
			}
		}
	}
	return label
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
	Allowed bool                 `json:"allowed"`
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
		if !item.Allowed {
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

func (s *ToolServer) getQualityProfiles(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		Service    string `json:"service"`
		InstanceID string `json:"instance_id"`
		ProfileID  int    `json:"profile_id"`
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
			version := "this build may predate custom formats"
			if params.Service == "sonarr" {
				version = "Sonarr gained custom formats in v4, so a v3 instance has no such endpoint"
			}
			return &ToolResult{Text: fmt.Sprintf(
				"%s returned 404 for the custom format endpoint: %s, or the stored instance URL is missing the service's URL base.",
				label, version)}, nil
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

func isCustomFormatsNotFound(err error) bool {
	return errors.Is(err, radarr.ErrCustomFormatsNotFound) ||
		errors.Is(err, sonarr.ErrCustomFormatsNotFound) ||
		errors.Is(err, chaptarr.ErrCustomFormatsNotFound)
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
