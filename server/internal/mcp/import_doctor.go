package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// --- Import Doctor: shared signal mapping ---

// sonarrSignal projects a Sonarr queue item into the neutral classifier input.
func sonarrSignal(item sonarr.DetailedQueueItem) arr.QueueSignal {
	messages := make([]arr.StatusMessage, 0, len(item.StatusMessages))
	for _, m := range item.StatusMessages {
		messages = append(messages, arr.StatusMessage{Title: m.Title, Messages: m.Messages})
	}
	return arr.QueueSignal{
		Status:                item.Status,
		TrackedDownloadStatus: item.TrackedDownloadStatus,
		TrackedDownloadState:  item.TrackedDownloadState,
		ErrorMessage:          item.ErrorMessage,
		StatusMessages:        messages,
		Protocol:              item.Protocol,
	}
}

// radarrSignal projects a Radarr queue item into the neutral classifier input.
func radarrSignal(item radarr.DetailedQueueItem) arr.QueueSignal {
	messages := make([]arr.StatusMessage, 0, len(item.StatusMessages))
	for _, m := range item.StatusMessages {
		messages = append(messages, arr.StatusMessage{Title: m.Title, Messages: m.Messages})
	}
	return arr.QueueSignal{
		Status:                item.Status,
		TrackedDownloadStatus: item.TrackedDownloadStatus,
		TrackedDownloadState:  item.TrackedDownloadState,
		ErrorMessage:          item.ErrorMessage,
		StatusMessages:        messages,
		Protocol:              item.Protocol,
	}
}

func sonarrQueueTitle(item sonarr.DetailedQueueItem) string {
	if item.Series != nil {
		if item.Episode != nil {
			title := fmt.Sprintf("%s S%02dE%02d", item.Series.Title, item.Episode.SeasonNumber, item.Episode.EpisodeNumber)
			if item.Episode.Title != "" {
				title += fmt.Sprintf(" %q", item.Episode.Title)
			}
			return title
		}
		return item.Series.Title
	}
	if item.Title != "" {
		return item.Title
	}
	return fmt.Sprintf("series %d", item.SeriesID)
}

func radarrQueueTitle(item radarr.DetailedQueueItem) string {
	if item.Movie != nil {
		return fmt.Sprintf("%s (%d)", item.Movie.Title, item.Movie.Year)
	}
	if item.Title != "" {
		return item.Title
	}
	return fmt.Sprintf("movie %d", item.MovieID)
}

// renderDiagnosis appends a problem item's diagnosis to the builder, including
// the exact next MCP tool call(s) to run for each suggested action so a weak
// agent can execute verbatim. tmdbID is the resolved TMDB id of the underlying
// movie/series (0 when it could not be resolved cheaply, e.g. a Sonarr queue
// item only carries a TVDB id).
func renderDiagnosis(sb *strings.Builder, mediaType string, queueID, tmdbID int, title string, d arr.Diagnosis) {
	fmt.Fprintf(sb, "\n\n- [queue %d] (%s) %s\n  problem: %s [%s]", queueID, mediaType, title, d.Problem, d.Severity)
	if d.Transparency != "" {
		fmt.Fprintf(sb, "\n  what happened: %s", d.Transparency)
	}
	actions := make([]string, 0, len(d.SuggestedActions))
	for _, a := range d.SuggestedActions {
		if a != arr.ActionNone {
			actions = append(actions, a)
		}
	}
	if len(actions) > 0 {
		fmt.Fprintf(sb, "\n  suggested actions: %s", strings.Join(actions, ", "))
	}
	if next := nextCalls(mediaType, queueID, tmdbID, d.SuggestedActions); next != "" {
		fmt.Fprintf(sb, "\n  → next: %s", next)
	}
}

// nextCalls maps a diagnosis's suggested action verbs to the precise MCP tool
// call(s) to run, with JSON args, joined with " then " when an action needs
// more than one step. It renders only the FIRST actionable verb (the primary
// fix); the rest stay visible in "suggested actions" as alternatives. Returns
// "" when there is nothing actionable.
func nextCalls(mediaType string, queueID, tmdbID int, verbs []string) string {
	for _, v := range verbs {
		switch v {
		case arr.ActionProcess, arr.ActionRescan:
			// rescan_media runs Rescan{Series,Movie} then the import pass. It
			// needs a tmdb_id; the Sonarr queue item only carries a TVDB id, so
			// fall back to naming the tool when we could not resolve one.
			if tmdbID != 0 {
				return toolCall("rescan_media", fmt.Sprintf(`{"tmdb_id": %d, "media_type": %q}`, tmdbID, mediaType))
			}
			return "rescan_media (resolve this item's tmdb_id first — get_library media_type=" + mediaType + " by title — then call it with that tmdb_id)"
		case arr.ActionManualImport:
			return toolCall("get_manual_import_candidates", fmt.Sprintf(`{"queue_id": %d, "media_type": %q}`, queueID, mediaType)) +
				" then " + toolCall("execute_manual_import", fmt.Sprintf(`{"queue_id": %d, "media_type": %q}`, queueID, mediaType))
		case arr.ActionForceImport:
			return toolCall("execute_manual_import", fmt.Sprintf(`{"queue_id": %d, "media_type": %q, "force": true}`, queueID, mediaType))
		case arr.ActionRemove, arr.ActionBlocklistSearch, arr.ActionChangeCategory:
			return toolCall("remediate_queue_item", fmt.Sprintf(`{"queue_id": %d, "media_type": %q, "action": %q}`, queueID, mediaType, v))
		}
	}
	return ""
}

// toolCall renders a "name args" call fragment for the next-step line.
func toolCall(name, args string) string {
	return name + " " + args
}

// --- get_arr_health ---

// renderHealthSection renders a service's non-ok health checks (ok-type checks
// are skipped so the agent only sees actionable config problems). The generic
// parameter lets the one renderer serve both sonarr.HealthCheck and
// radarr.HealthCheck, which are structurally identical but distinct types.
func renderHealthSection[T sonarr.HealthCheck | radarr.HealthCheck](label string, checks []T) string {
	var sb strings.Builder
	shown := 0
	for _, c := range checks {
		var source, ctype, message, wiki string
		switch v := any(c).(type) {
		case sonarr.HealthCheck:
			source, ctype, message, wiki = v.Source, v.Type, v.Message, v.WikiURL
		case radarr.HealthCheck:
			source, ctype, message, wiki = v.Source, v.Type, v.Message, v.WikiURL
		}
		if strings.EqualFold(ctype, "ok") {
			continue
		}
		if shown == 0 {
			fmt.Fprintf(&sb, "%s health:", label)
		}
		shown++
		fmt.Fprintf(&sb, "\n- [%s] %s", strings.ToLower(ctype), message)
		if source != "" {
			fmt.Fprintf(&sb, " (%s)", source)
		}
		if wiki != "" {
			fmt.Fprintf(&sb, "\n  more: %s", wiki)
		}
	}
	if shown == 0 {
		fmt.Fprintf(&sb, "%s health: no warnings or errors.", label)
	}
	return sb.String()
}

func (s *ToolServer) getArrHealth(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		MediaType string `json:"media_type"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	mediaType := normalizeMediaType(params.MediaType)

	var sections []string

	if mediaType == "movie" || mediaType == "all" {
		client := s.GetRadarr()
		if client == nil {
			if mediaType == "movie" {
				return &ToolResult{Text: "Radarr is not configured."}, nil
			}
			sections = append(sections, "Radarr is not configured.")
		} else {
			checks, err := client.GetHealth()
			if err != nil {
				return nil, err
			}
			sections = append(sections, renderHealthSection("Radarr", checks))
		}
	}

	if mediaType == "tv" || mediaType == "all" {
		client := s.GetSonarr()
		if client == nil {
			if mediaType == "tv" {
				return &ToolResult{Text: "Sonarr is not configured."}, nil
			}
			sections = append(sections, "Sonarr is not configured.")
		} else {
			checks, err := client.GetHealth()
			if err != nil {
				return nil, err
			}
			sections = append(sections, renderHealthSection("Sonarr", checks))
		}
	}

	return &ToolResult{Text: strings.Join(sections, "\n\n")}, nil
}

// --- diagnose_queue ---

func (s *ToolServer) diagnoseQueue(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		MediaType string `json:"media_type"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	mediaType := normalizeMediaType(params.MediaType)

	var sb strings.Builder
	var notes []string
	problems := 0
	healthy := 0

	if mediaType == "movie" || mediaType == "all" {
		radarrClient := s.GetRadarr()
		if radarrClient == nil {
			if mediaType == "movie" {
				return &ToolResult{Text: "Radarr is not configured."}, nil
			}
			notes = append(notes, "Radarr is not configured.")
		} else {
			items, err := radarrClient.GetQueueDetailed()
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				d := arr.Diagnose(radarrSignal(item))
				if d.Severity == arr.SeverityOK {
					healthy++
					continue
				}
				problems++
				// Radarr queue items embed the movie's TMDB id, so rescan_media
				// calls can be rendered fully resolved.
				tmdbID := 0
				if item.Movie != nil {
					tmdbID = item.Movie.TmdbID
				}
				renderDiagnosis(&sb, "movie", item.ID, tmdbID, radarrQueueTitle(item), d)
			}
		}
	}

	if mediaType == "tv" || mediaType == "all" {
		sonarrClient := s.GetSonarr()
		if sonarrClient == nil {
			if mediaType == "tv" {
				return &ToolResult{Text: "Sonarr is not configured."}, nil
			}
			notes = append(notes, "Sonarr is not configured.")
		} else {
			items, err := sonarrClient.GetQueueDetailed()
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				d := arr.Diagnose(sonarrSignal(item))
				if d.Severity == arr.SeverityOK {
					healthy++
					continue
				}
				problems++
				// Sonarr queue items carry only a TVDB id (no TMDB), so we
				// cannot resolve a tmdb_id cheaply here; pass 0 and let
				// nextCalls name the tool with a resolve hint instead.
				renderDiagnosis(&sb, "tv", item.ID, 0, sonarrQueueTitle(item), d)
			}
		}
	}

	var header strings.Builder
	if problems == 0 {
		header.WriteString("Import Doctor: no queue problems found.")
		if healthy > 0 {
			fmt.Fprintf(&header, " %d item(s) are downloading or importing normally.", healthy)
		}
	} else {
		fmt.Fprintf(&header, "Import Doctor found %d queue item(s) needing attention", problems)
		if healthy > 0 {
			fmt.Fprintf(&header, " (%d other item(s) are healthy)", healthy)
		}
		header.WriteString(". Each item lists the exact next tool call to run on its \"→ next:\" line (get_manual_import_candidates, execute_manual_import, remediate_queue_item, or rescan_media). For path/permission/client errors, get_arr_health confirms the config root cause:")
	}
	if len(notes) > 0 {
		fmt.Fprintf(&header, " (%s)", strings.Join(notes, " "))
	}

	return &ToolResult{Text: header.String() + sb.String()}, nil
}

// --- queue item lookup ---

// findRadarrQueueItem returns the queue item with the given id, or nil.
func findRadarrQueueItem(client *radarr.Client, queueID int) (*radarr.DetailedQueueItem, error) {
	items, err := client.GetQueueDetailed()
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == queueID {
			return &items[i], nil
		}
	}
	return nil, nil
}

// findSonarrQueueItem returns the queue item with the given id, or nil.
func findSonarrQueueItem(client *sonarr.Client, queueID int) (*sonarr.DetailedQueueItem, error) {
	items, err := client.GetQueueDetailed()
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == queueID {
			return &items[i], nil
		}
	}
	return nil, nil
}

// --- get_manual_import_candidates ---

func formatRejections(rejections []arr.ManualImportRejectionView) string {
	if len(rejections) == 0 {
		return ""
	}
	parts := make([]string, 0, len(rejections))
	for _, r := range rejections {
		if r.Type != "" {
			parts = append(parts, fmt.Sprintf("%s (%s)", r.Reason, r.Type))
		} else {
			parts = append(parts, r.Reason)
		}
	}
	return strings.Join(parts, "; ")
}

func (s *ToolServer) getManualImportCandidates(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		QueueID   int    `json:"queue_id"`
		MediaType string `json:"media_type"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}

	switch params.MediaType {
	case "movie":
		client := s.GetRadarr()
		if client == nil {
			return &ToolResult{Text: "Radarr is not configured."}, nil
		}
		item, err := findRadarrQueueItem(client, params.QueueID)
		if err != nil {
			return nil, err
		}
		if item == nil {
			return &ToolResult{Text: fmt.Sprintf("No movie queue item with id %d. Run get_queue or diagnose_queue for current ids.", params.QueueID)}, nil
		}
		if item.DownloadID == "" {
			return &ToolResult{Text: fmt.Sprintf("Queue item %d has no download-client id yet, so its files cannot be inspected. Wait until it has been handed to the download client.", params.QueueID)}, nil
		}
		candidates, err := client.GetManualImportCandidates(item.DownloadID)
		if err != nil {
			return nil, err
		}
		if len(candidates) == 0 {
			return &ToolResult{Text: fmt.Sprintf("No importable files found for %s. The folder may be empty, an unextracted archive, or inaccessible.", radarrQueueTitle(*item))}, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d candidate file(s) for %s:", len(candidates), radarrQueueTitle(*item))
		for _, c := range candidates {
			fmt.Fprintf(&sb, "\n- %s (%s)", c.Name, humanBytes(float64(c.Size)))
			if c.MovieID != 0 {
				fmt.Fprintf(&sb, "\n  maps to movie id %d", c.MovieID)
			} else {
				sb.WriteString("\n  not matched to a movie")
			}
			if rej := formatRejections(toRejectionViews(c.Rejections)); rej != "" {
				fmt.Fprintf(&sb, "\n  rejections: %s", rej)
			}
		}
		sb.WriteString("\n\nUse execute_manual_import to import these (add force=true to import despite permanent rejections).")
		return &ToolResult{Text: sb.String()}, nil

	case "tv":
		client := s.GetSonarr()
		if client == nil {
			return &ToolResult{Text: "Sonarr is not configured."}, nil
		}
		item, err := findSonarrQueueItem(client, params.QueueID)
		if err != nil {
			return nil, err
		}
		if item == nil {
			return &ToolResult{Text: fmt.Sprintf("No TV queue item with id %d. Run get_queue or diagnose_queue for current ids.", params.QueueID)}, nil
		}
		if item.DownloadID == "" {
			return &ToolResult{Text: fmt.Sprintf("Queue item %d has no download-client id yet, so its files cannot be inspected. Wait until it has been handed to the download client.", params.QueueID)}, nil
		}
		candidates, err := client.GetManualImportCandidates(item.DownloadID)
		if err != nil {
			return nil, err
		}
		if len(candidates) == 0 {
			return &ToolResult{Text: fmt.Sprintf("No importable files found for %s. The folder may be empty, an unextracted archive, or inaccessible.", sonarrQueueTitle(*item))}, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d candidate file(s) for %s:", len(candidates), sonarrQueueTitle(*item))
		for _, c := range candidates {
			fmt.Fprintf(&sb, "\n- %s (%s)", c.Name, humanBytes(float64(c.Size)))
			if len(c.Episodes) > 0 {
				eps := make([]string, 0, len(c.Episodes))
				for _, e := range c.Episodes {
					eps = append(eps, fmt.Sprintf("S%02dE%02d", e.SeasonNumber, e.EpisodeNumber))
				}
				fmt.Fprintf(&sb, "\n  maps to %s", strings.Join(eps, ", "))
			} else {
				sb.WriteString("\n  not matched to any episode (cannot be imported without episode mapping)")
			}
			if rej := formatRejections(toRejectionViews(c.Rejections)); rej != "" {
				fmt.Fprintf(&sb, "\n  rejections: %s", rej)
			}
		}
		sb.WriteString("\n\nUse execute_manual_import to import these (add force=true to import despite permanent rejections).")
		return &ToolResult{Text: sb.String()}, nil

	default:
		return &ToolResult{Text: "media_type must be \"movie\" or \"tv\"."}, nil
	}
}

func toRejectionViews[T sonarr.ManualImportRejection | radarr.ManualImportRejection](rejections []T) []arr.ManualImportRejectionView {
	out := make([]arr.ManualImportRejectionView, 0, len(rejections))
	for _, r := range rejections {
		switch v := any(r).(type) {
		case sonarr.ManualImportRejection:
			out = append(out, arr.ManualImportRejectionView{Reason: v.Reason, Type: v.Type})
		case radarr.ManualImportRejection:
			out = append(out, arr.ManualImportRejectionView{Reason: v.Reason, Type: v.Type})
		}
	}
	return out
}

// hasPermanentRejection reports whether any rejection is permanent.
func hasPermanentRejection(rejections []arr.ManualImportRejectionView) bool {
	for _, r := range rejections {
		if strings.EqualFold(r.Type, "permanent") {
			return true
		}
	}
	return false
}

// --- execute_manual_import ---

func (s *ToolServer) executeManualImport(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		QueueID   int    `json:"queue_id"`
		MediaType string `json:"media_type"`
		Force     bool   `json:"force"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}

	switch params.MediaType {
	case "movie":
		client := s.GetRadarr()
		if client == nil {
			return &ToolResult{Text: "Radarr is not configured."}, nil
		}
		item, err := findRadarrQueueItem(client, params.QueueID)
		if err != nil {
			return nil, err
		}
		if item == nil {
			return &ToolResult{Text: fmt.Sprintf("No movie queue item with id %d.", params.QueueID)}, nil
		}
		if item.DownloadID == "" {
			return &ToolResult{Text: fmt.Sprintf("Queue item %d has no download-client id yet; nothing to import.", params.QueueID)}, nil
		}
		candidates, err := client.GetManualImportCandidates(item.DownloadID)
		if err != nil {
			return nil, err
		}
		var files []radarr.ManualImportFile
		var skipped []string
		for _, c := range candidates {
			rejections := toRejectionViews(c.Rejections)
			if !params.Force && hasPermanentRejection(rejections) {
				skipped = append(skipped, fmt.Sprintf("%s (%s)", c.Name, formatRejections(rejections)))
				continue
			}
			if c.MovieID == 0 {
				skipped = append(skipped, fmt.Sprintf("%s (not matched to a movie)", c.Name))
				continue
			}
			files = append(files, radarr.ManualImportFile{
				Path:         c.Path,
				FolderName:   c.FolderName,
				MovieID:      c.MovieID,
				Quality:      c.Quality,
				Languages:    c.Languages,
				ReleaseGroup: c.ReleaseGroup,
				DownloadID:   c.DownloadID,
				IndexerFlags: c.IndexerFlags,
			})
		}
		if len(files) == 0 {
			return &ToolResult{Text: importSkippedMessage(skipped, params.Force)}, nil
		}
		importMode := importModeFor(item.Protocol)
		if err := client.ExecuteManualImport(files, importMode); err != nil {
			return nil, err
		}
		return &ToolResult{Text: importResultMessage(len(files), importMode, skipped)}, nil

	case "tv":
		client := s.GetSonarr()
		if client == nil {
			return &ToolResult{Text: "Sonarr is not configured."}, nil
		}
		item, err := findSonarrQueueItem(client, params.QueueID)
		if err != nil {
			return nil, err
		}
		if item == nil {
			return &ToolResult{Text: fmt.Sprintf("No TV queue item with id %d.", params.QueueID)}, nil
		}
		if item.DownloadID == "" {
			return &ToolResult{Text: fmt.Sprintf("Queue item %d has no download-client id yet; nothing to import.", params.QueueID)}, nil
		}
		candidates, err := client.GetManualImportCandidates(item.DownloadID)
		if err != nil {
			return nil, err
		}
		var files []sonarr.ManualImportFile
		var skipped []string
		for _, c := range candidates {
			rejections := toRejectionViews(c.Rejections)
			if !params.Force && hasPermanentRejection(rejections) {
				skipped = append(skipped, fmt.Sprintf("%s (%s)", c.Name, formatRejections(rejections)))
				continue
			}
			episodeIDs := make([]int, 0, len(c.Episodes))
			for _, e := range c.Episodes {
				if e.ID != 0 {
					episodeIDs = append(episodeIDs, e.ID)
				}
			}
			if len(episodeIDs) == 0 {
				skipped = append(skipped, fmt.Sprintf("%s (no episode mapping)", c.Name))
				continue
			}
			files = append(files, sonarr.ManualImportFile{
				Path:         c.Path,
				FolderName:   c.FolderName,
				SeriesID:     c.SeriesID,
				EpisodeIDs:   episodeIDs,
				Quality:      c.Quality,
				Languages:    c.Languages,
				ReleaseGroup: c.ReleaseGroup,
				DownloadID:   c.DownloadID,
				IndexerFlags: c.IndexerFlags,
				ReleaseType:  c.ReleaseType,
			})
		}
		if len(files) == 0 {
			return &ToolResult{Text: importSkippedMessage(skipped, params.Force)}, nil
		}
		importMode := importModeFor(item.Protocol)
		if err := client.ExecuteManualImport(files, importMode); err != nil {
			return nil, err
		}
		return &ToolResult{Text: importResultMessage(len(files), importMode, skipped)}, nil

	default:
		return &ToolResult{Text: "media_type must be \"movie\" or \"tv\"."}, nil
	}
}

// importModeFor picks the lowercase ManualImport importMode: copy for torrents
// (preserves seeding/hardlinks), move for usenet and anything else.
func importModeFor(protocol string) string {
	if strings.EqualFold(protocol, "torrent") {
		return "copy"
	}
	return "move"
}

func importResultMessage(count int, importMode string, skipped []string) string {
	text := fmt.Sprintf("Sent %d file(s) to import (mode: %s). Check get_queue or diagnose_queue shortly to confirm they imported.", count, importMode)
	if len(skipped) > 0 {
		text += fmt.Sprintf("\nSkipped %d file(s): %s", len(skipped), strings.Join(skipped, "; "))
	}
	return text
}

func importSkippedMessage(skipped []string, force bool) string {
	if len(skipped) == 0 {
		return "Nothing to import: no candidate files were found."
	}
	text := fmt.Sprintf("Nothing imported. Skipped %d file(s): %s", len(skipped), strings.Join(skipped, "; "))
	if !force {
		text += "\nRetry with force=true to import despite permanent rejections."
	}
	return text
}

// --- remediate_queue_item ---

func (s *ToolServer) remediateQueueItem(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		QueueID   int    `json:"queue_id"`
		MediaType string `json:"media_type"`
		Action    string `json:"action"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	switch params.Action {
	case "remove", "blocklist_search", "change_category":
	default:
		return &ToolResult{Text: "action must be \"remove\", \"blocklist_search\", or \"change_category\"."}, nil
	}

	switch params.MediaType {
	case "movie":
		client := s.GetRadarr()
		if client == nil {
			return &ToolResult{Text: "Radarr is not configured."}, nil
		}
		item, err := findRadarrQueueItem(client, params.QueueID)
		if err != nil {
			return nil, err
		}
		if item == nil {
			return &ToolResult{Text: fmt.Sprintf("No movie queue item with id %d.", params.QueueID)}, nil
		}
		switch params.Action {
		case "remove":
			if err := client.RemoveQueueItem(params.QueueID, true, false, false, false); err != nil {
				return nil, err
			}
			return &ToolResult{Text: fmt.Sprintf("Removed queue item %d (%s) and deleted the download.", params.QueueID, radarrQueueTitle(*item))}, nil
		case "blocklist_search":
			if err := client.RemoveQueueItem(params.QueueID, true, true, false, false); err != nil {
				return nil, err
			}
			if item.MovieID != 0 {
				if err := client.TriggerMoviesSearch([]int{item.MovieID}); err != nil {
					return nil, err
				}
				return &ToolResult{Text: fmt.Sprintf("Removed and blocklisted queue item %d (%s) and started a fresh search for a different release.", params.QueueID, radarrQueueTitle(*item))}, nil
			}
			return &ToolResult{Text: fmt.Sprintf("Removed and blocklisted queue item %d (%s). Could not start a search: no movie id on the item.", params.QueueID, radarrQueueTitle(*item))}, nil
		case "change_category":
			if err := client.RemoveQueueItem(params.QueueID, false, false, false, true); err != nil {
				return nil, err
			}
			return &ToolResult{Text: fmt.Sprintf("Handed queue item %d (%s) to the download client's post-import category. It stays in the client for tools like Unpackerr.", params.QueueID, radarrQueueTitle(*item))}, nil
		}

	case "tv":
		client := s.GetSonarr()
		if client == nil {
			return &ToolResult{Text: "Sonarr is not configured."}, nil
		}
		item, err := findSonarrQueueItem(client, params.QueueID)
		if err != nil {
			return nil, err
		}
		if item == nil {
			return &ToolResult{Text: fmt.Sprintf("No TV queue item with id %d.", params.QueueID)}, nil
		}
		switch params.Action {
		case "remove":
			if err := client.RemoveQueueItem(params.QueueID, true, false, false, false); err != nil {
				return nil, err
			}
			return &ToolResult{Text: fmt.Sprintf("Removed queue item %d (%s) and deleted the download.", params.QueueID, sonarrQueueTitle(*item))}, nil
		case "blocklist_search":
			if err := client.RemoveQueueItem(params.QueueID, true, true, false, false); err != nil {
				return nil, err
			}
			if item.EpisodeID != 0 {
				if err := client.TriggerEpisodeSearch([]int{item.EpisodeID}); err != nil {
					return nil, err
				}
				return &ToolResult{Text: fmt.Sprintf("Removed and blocklisted queue item %d (%s) and started a fresh search for a different release.", params.QueueID, sonarrQueueTitle(*item))}, nil
			}
			return &ToolResult{Text: fmt.Sprintf("Removed and blocklisted queue item %d (%s). Could not start a search: no episode id on the item.", params.QueueID, sonarrQueueTitle(*item))}, nil
		case "change_category":
			if err := client.RemoveQueueItem(params.QueueID, false, false, false, true); err != nil {
				return nil, err
			}
			return &ToolResult{Text: fmt.Sprintf("Handed queue item %d (%s) to the download client's post-import category. It stays in the client for tools like Unpackerr.", params.QueueID, sonarrQueueTitle(*item))}, nil
		}

	default:
		return &ToolResult{Text: "media_type must be \"movie\" or \"tv\"."}, nil
	}
	return &ToolResult{Text: "No remediation was applied."}, nil
}

// --- rescan_media ---

func (s *ToolServer) rescanMedia(input json.RawMessage) (*ToolResult, error) {
	var params struct {
		TmdbID    int    `json:"tmdb_id"`
		MediaType string `json:"media_type"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}

	switch params.MediaType {
	case "movie":
		client := s.GetRadarr()
		if client == nil {
			return &ToolResult{Text: "Radarr is not configured."}, nil
		}
		movie, err := client.GetMovieByTMDB(params.TmdbID)
		if err != nil {
			return nil, err
		}
		if movie == nil {
			return &ToolResult{Text: "This movie is not in the library."}, nil
		}
		if err := client.RescanMovie(movie.ID); err != nil {
			return nil, err
		}
		if err := client.ProcessMonitoredDownloads(); err != nil {
			return nil, err
		}
		return &ToolResult{Text: fmt.Sprintf("Rescanning %s (%d) and running the import pass. Check diagnose_queue shortly.", movie.Title, movie.Year)}, nil

	case "tv":
		client := s.GetSonarr()
		if client == nil {
			return &ToolResult{Text: "Sonarr is not configured."}, nil
		}
		series, err := s.findSeriesByTMDB(client, params.TmdbID)
		if err != nil {
			return nil, err
		}
		if series == nil {
			return &ToolResult{Text: "This show is not in the library."}, nil
		}
		if err := client.RescanSeries(series.ID); err != nil {
			return nil, err
		}
		if err := client.ProcessMonitoredDownloads(); err != nil {
			return nil, err
		}
		return &ToolResult{Text: fmt.Sprintf("Rescanning %s and running the import pass. Check diagnose_queue shortly.", series.Title)}, nil

	default:
		return &ToolResult{Text: "media_type must be \"movie\" or \"tv\"."}, nil
	}
}
