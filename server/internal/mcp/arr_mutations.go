package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

// PartialMutationError reports a compound operation whose first mutation was
// accepted by the arr but whose follow-up failed. Callers must not describe this
// as a clean failure or retry the whole sequence blindly.
type PartialMutationError struct {
	Completed string
	Pending   string
	Err       error
}

func (e *PartialMutationError) Error() string {
	return fmt.Sprintf("%s; %s failed: %v", e.Completed, e.Pending, e.Err)
}

func (e *PartialMutationError) Unwrap() error         { return e.Err }
func (e *PartialMutationError) PartialMutation() bool { return true }

// MutationNotStartedError is a definitive preflight/no-op outcome. It tells the
// approval service that no remote mutation was dispatched, so the action may be
// recorded as failed (never "executed" and never outcome-unknown).
type MutationNotStartedError struct {
	Detail string
	Cause  error
}

func (e *MutationNotStartedError) Error() string            { return e.Detail }
func (e *MutationNotStartedError) Unwrap() error            { return e.Cause }
func (e *MutationNotStartedError) MutationNotStarted() bool { return true }

func mutationNotStarted(detail string) (string, error) {
	return "", &MutationNotStartedError{Detail: detail}
}

// CustomFormatMutator is the complete remote surface used by the canonical
// custom-format upsert. Radarr, Sonarr, and Chaptarr clients satisfy it.
type CustomFormatMutator interface {
	GetCustomFormatsRawContext(context.Context) ([]json.RawMessage, error)
	GetQualityProfilesRawContext(context.Context) ([]json.RawMessage, error)
	CreateCustomFormatRawContext(context.Context, json.RawMessage) (json.RawMessage, error)
	UpdateCustomFormatRawContext(context.Context, int, json.RawMessage) (json.RawMessage, error)
}

// CustomFormatUpsertResult identifies the one remote record changed by an
// UpsertCustomFormatHelper call.
type CustomFormatUpsertResult struct {
	Action string
	ID     int
	Name   string
}

// SettingsWriteGuard is called after the authoritative read/merge and
// immediately before the one remote write. Interactive callers use it to
// re-check live authorization, enablement, and instance binding after any
// slow preflight; gated executors can enforce their own approval binding.
type SettingsWriteGuard func(context.Context) error

type customFormatHead struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

const (
	maxCustomFormatPayloadBytes   = 1 << 20
	maxCustomFormatNameBytes      = 256
	maxCustomFormatSpecifications = 256
)

// UpsertCustomFormatHelper is the single mutation body for creating or
// updating a custom format. It matches live records by name, transforms the
// TRaSH fields-object shape, and always GETs before a full-object PUT so fields
// introduced by a newer arr are preserved.
func UpsertCustomFormatHelper(ctx context.Context, client CustomFormatMutator, payload json.RawMessage, beforeWrite SettingsWriteGuard) (CustomFormatUpsertResult, error) {
	if client == nil {
		return CustomFormatUpsertResult{}, &MutationNotStartedError{Detail: "arr custom-format client is not configured"}
	}
	if err := ctx.Err(); err != nil {
		return CustomFormatUpsertResult{}, &MutationNotStartedError{Detail: err.Error(), Cause: err}
	}
	incoming, name, err := normalizeCustomFormatPayload(payload)
	if err != nil {
		return CustomFormatUpsertResult{}, &MutationNotStartedError{Detail: err.Error()}
	}

	raws, err := client.GetCustomFormatsRawContext(ctx)
	if err != nil {
		return CustomFormatUpsertResult{}, &MutationNotStartedError{Detail: err.Error(), Cause: err}
	}
	target, err := findCustomFormatByName(raws, name)
	if err != nil {
		return CustomFormatUpsertResult{}, &MutationNotStartedError{Detail: err.Error()}
	}

	if target == nil {
		delete(incoming, "id")
		body, err := json.Marshal(incoming)
		if err != nil {
			return CustomFormatUpsertResult{}, &MutationNotStartedError{Detail: "could not encode the custom format"}
		}
		if err := ctx.Err(); err != nil {
			return CustomFormatUpsertResult{}, &MutationNotStartedError{Detail: err.Error(), Cause: err}
		}
		if beforeWrite != nil {
			if err := beforeWrite(ctx); err != nil {
				return CustomFormatUpsertResult{}, &MutationNotStartedError{Detail: err.Error(), Cause: err}
			}
		}
		createdRaw, err := client.CreateCustomFormatRawContext(ctx, body)
		if err != nil {
			return CustomFormatUpsertResult{}, classifySettingsWriteOutcome("the custom format create may have been accepted", err)
		}
		created, parseErr := decodeCustomFormatHead(createdRaw)
		if parseErr == nil && created.Name != name {
			parseErr = fmt.Errorf("create response named a different custom format")
		}
		if parseErr != nil {
			// Some compatible builds return an empty/minimal 2xx response. Re-read
			// the authoritative collection before telling the caller to retry a
			// create that may already have succeeded.
			current, getErr := client.GetCustomFormatsRawContext(ctx)
			if getErr != nil {
				return CustomFormatUpsertResult{}, &PartialMutationError{Completed: "the custom format create was accepted", Pending: "confirming the created record", Err: getErr}
			}
			confirmed, getErr := findCustomFormatByName(current, name)
			if getErr != nil || confirmed == nil {
				if getErr == nil {
					getErr = parseErr
				}
				return CustomFormatUpsertResult{}, &PartialMutationError{Completed: "the custom format create was accepted", Pending: "confirming the created record", Err: getErr}
			}
			created = &confirmed.customFormatHead
		}
		profiles, profileErr := client.GetQualityProfilesRawContext(ctx)
		if profileErr == nil {
			profileErr = verifyCreatedCustomFormatProfileScores(profiles, created.ID)
		}
		if profileErr != nil {
			return CustomFormatUpsertResult{}, &PartialMutationError{
				Completed: fmt.Sprintf("custom format %d (%q) was created", created.ID, created.Name),
				Pending:   "verifying that every quality profile received it at score 0",
				Err:       profileErr,
			}
		}
		return CustomFormatUpsertResult{Action: "created", ID: created.ID, Name: created.Name}, nil
	}

	live, err := decodeJSONObject(target.raw)
	if err != nil {
		return CustomFormatUpsertResult{}, &MutationNotStartedError{Detail: fmt.Sprintf("custom format %d (%q) could not be decoded safely", target.ID, target.Name)}
	}
	for key, value := range incoming {
		live[key] = value
	}
	live["id"] = json.Number(strconv.Itoa(target.ID))
	body, err := json.Marshal(live)
	if err != nil {
		return CustomFormatUpsertResult{}, &MutationNotStartedError{Detail: "could not encode the custom format update"}
	}
	if err := ctx.Err(); err != nil {
		return CustomFormatUpsertResult{}, &MutationNotStartedError{Detail: err.Error(), Cause: err}
	}
	if beforeWrite != nil {
		if err := beforeWrite(ctx); err != nil {
			return CustomFormatUpsertResult{}, &MutationNotStartedError{Detail: err.Error(), Cause: err}
		}
	}
	if _, err := client.UpdateCustomFormatRawContext(ctx, target.ID, body); err != nil {
		return CustomFormatUpsertResult{}, classifySettingsWriteOutcome("the custom format update may have been accepted", err)
	}
	return CustomFormatUpsertResult{Action: "updated", ID: target.ID, Name: name}, nil
}

func verifyCreatedCustomFormatProfileScores(raws []json.RawMessage, formatID int) error {
	for _, raw := range raws {
		var profile struct {
			ID          int `json:"id"`
			FormatItems []struct {
				Format int  `json:"format"`
				Score  *int `json:"score"`
			} `json:"formatItems"`
		}
		if err := json.Unmarshal(raw, &profile); err != nil || profile.ID <= 0 {
			return fmt.Errorf("a quality profile could not be decoded safely")
		}
		matches := 0
		for _, item := range profile.FormatItems {
			if item.Format != formatID {
				continue
			}
			matches++
			if item.Score == nil || *item.Score != 0 {
				return fmt.Errorf("quality profile %d did not receive custom format %d at score 0", profile.ID, formatID)
			}
		}
		if matches != 1 {
			return fmt.Errorf("quality profile %d contains custom format %d %d times, want exactly once", profile.ID, formatID, matches)
		}
	}
	return nil
}

type liveCustomFormat struct {
	customFormatHead
	raw json.RawMessage
}

func findCustomFormatByName(raws []json.RawMessage, name string) (*liveCustomFormat, error) {
	formats := make([]liveCustomFormat, 0, len(raws))
	for _, raw := range raws {
		head, err := decodeCustomFormatHead(raw)
		if err != nil {
			return nil, fmt.Errorf("an existing custom format has an unreadable id or name; no write was attempted")
		}
		formats = append(formats, liveCustomFormat{customFormatHead: *head, raw: raw})
	}

	var exact []*liveCustomFormat
	for i := range formats {
		if formats[i].Name == name {
			exact = append(exact, &formats[i])
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return nil, fmt.Errorf("multiple existing custom formats are named %q; no write was attempted", name)
	}

	return nil, nil
}

func decodeCustomFormatHead(raw json.RawMessage) (*customFormatHead, error) {
	var head customFormatHead
	if err := json.Unmarshal(raw, &head); err != nil || head.ID <= 0 || strings.TrimSpace(head.Name) == "" {
		return nil, fmt.Errorf("invalid custom format identity")
	}
	return &head, nil
}

func normalizeCustomFormatPayload(payload json.RawMessage) (map[string]any, string, error) {
	if len(payload) > maxCustomFormatPayloadBytes {
		return nil, "", fmt.Errorf("custom_format exceeds the 1 MiB size limit")
	}
	object, err := decodeJSONObject(payload)
	if err != nil {
		return nil, "", fmt.Errorf("custom_format must be exactly one JSON object")
	}
	name, ok := object["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return nil, "", fmt.Errorf("custom_format.name must be a nonblank string")
	}
	if len(name) > maxCustomFormatNameBytes {
		return nil, "", fmt.Errorf("custom_format.name exceeds the 256-byte limit")
	}

	specifications, ok := object["specifications"].([]any)
	if !ok {
		return nil, "", fmt.Errorf("custom_format.specifications must be an array")
	}
	if len(specifications) > maxCustomFormatSpecifications {
		return nil, "", fmt.Errorf("custom_format.specifications exceeds the 256-item limit")
	}
	for i, value := range specifications {
		specification, ok := value.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("custom_format.specifications[%d] must be an object", i)
		}
		fields, exists := specification["fields"]
		if !exists {
			return nil, "", fmt.Errorf("custom_format.specifications[%d].fields is required", i)
		}
		switch typed := fields.(type) {
		case []any:
			// Native arr shape; preserve it exactly as decoded.
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			converted := make([]any, 0, len(keys))
			for _, key := range keys {
				converted = append(converted, map[string]any{"name": key, "value": typed[key]})
			}
			specification["fields"] = converted
		default:
			return nil, "", fmt.Errorf("custom_format.specifications[%d].fields must be an array or TRaSH-style object", i)
		}
	}
	object["specifications"] = specifications
	return object, name, nil
}

func decodeJSONObject(raw json.RawMessage) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("invalid JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON value")
	}
	return object, nil
}

func classifySettingsWriteOutcome(completed string, err error) error {
	var unknown interface{ SettingsWriteOutcomeUnknown() bool }
	if errors.As(err, &unknown) && unknown.SettingsWriteOutcomeUnknown() {
		return &PartialMutationError{Completed: completed, Pending: "confirming the live settings", Err: err}
	}
	return err
}

// This file holds the SHARED arr-mutation helpers. Each is the single, canonical
// body for one consequential arr mutation. Existing remediation mutations have
// two callers:
//
//   - the manual MCP fix tools (grab_release / remove_queue_item /
//     remediate_queue_item / execute_manual_import / trigger_search / rescan_media)
//     an admin drives from the AI chat, and
//   - remediation.Executor, which replays an admin-APPROVED proposal.
//
// Settings mutations are also defined here before any second caller exists, so
// a future gate/executor must reuse this same body rather than cloning it.
// Extracting the bodies here means there is exactly one code path per mutation.
// The helpers take already-resolved typed clients plus typed args. A settings
// helper may do the authoritative reads needed for a safe full-object write and
// invokes its caller-supplied guard immediately before dispatch; RBAC or
// approval policy remains in the caller rather than being duplicated here.
//
// A nil client for the relevant media_type yields the same "<service> is not
// configured." message the tools produced, so behavior is identical.

// seriesByTMDB resolves a Sonarr series from a TMDB id, preferring the cached
// tmdb<->tvdb mapping and falling back to a full series scan. It is the single
// implementation behind both the read tools' (s *ToolServer).findSeriesByTMDB
// wrapper and the mutation helpers below.
func seriesByTMDB(bridge *tmdb.Bridge, client *sonarr.Client, tmdbID int) (*sonarr.Series, error) {
	if bridge != nil {
		if res, err := bridge.ResolveTVDBID(tmdbID); err == nil && res.TVDBID != 0 {
			if series, err := client.GetSeriesByTVDB(res.TVDBID); err == nil && series != nil {
				return series, nil
			}
		}
	}
	all, err := client.GetAllSeries()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].TmdbID != 0 && all[i].TmdbID == tmdbID {
			return &all[i], nil
		}
	}
	return nil, nil
}

// GrabReleaseHelper sends a specific release (by guid + indexer id) to the
// download client, optionally replacing an existing queue item first. It is the
// shared body of the grab_release tool and the Executor's grab_release kind.
//
// queueIDToReplace > 0 removes that queue item (deleting the download from the
// client, no blocklist) before grabbing, so a "grab a different release" fix
// doesn't leave the old one downloading alongside the new one.
func GrabReleaseHelper(rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client, mediaType, guid string, indexerID, queueIDToReplace int) (string, error) {
	if guid == "" {
		return mutationNotStarted("guid is required (from search_releases)")
	}
	switch mediaType {
	case "movie":
		if rc == nil {
			return mutationNotStarted("Radarr is not configured")
		}
		if queueIDToReplace > 0 {
			if err := rc.RemoveQueueItem(queueIDToReplace, true, false, false, false); err != nil {
				return "", err
			}
		}
		if err := rc.GrabRelease(guid, indexerID); err != nil {
			if queueIDToReplace > 0 {
				return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed", queueIDToReplace), Pending: "sending the replacement release", Err: err}
			}
			return "", err
		}
	case "tv":
		if sc == nil {
			return mutationNotStarted("Sonarr is not configured")
		}
		if queueIDToReplace > 0 {
			if err := sc.RemoveQueueItem(queueIDToReplace, true, false, false, false); err != nil {
				return "", err
			}
		}
		if err := sc.GrabRelease(guid, indexerID); err != nil {
			if queueIDToReplace > 0 {
				return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed", queueIDToReplace), Pending: "sending the replacement release", Err: err}
			}
			return "", err
		}
	case "book":
		if cc == nil {
			return mutationNotStarted("Chaptarr is not configured")
		}
		if queueIDToReplace > 0 {
			if err := cc.RemoveQueueItem(queueIDToReplace, true, false, false, false); err != nil {
				return "", err
			}
		}
		if err := cc.GrabRelease(guid, indexerID); err != nil {
			if queueIDToReplace > 0 {
				return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed", queueIDToReplace), Pending: "sending the replacement release", Err: err}
			}
			return "", err
		}
	default:
		return mutationNotStarted("media_type must be \"movie\", \"tv\", or \"book\"")
	}
	msg := "Release sent to the download client. It should show up in get_queue shortly."
	if queueIDToReplace > 0 {
		msg = fmt.Sprintf("Removed queue item %d and sent the new release to the download client. It should show up in get_queue shortly.", queueIDToReplace)
	}
	return msg, nil
}

// RemoveQueueItemHelper removes a queue item (deleting the download from the
// client), optionally blocklisting the release so it is not re-grabbed. Shared
// body of the remove_queue_item tool.
func RemoveQueueItemHelper(rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client, mediaType string, queueID int, blocklist bool) (string, error) {
	switch mediaType {
	case "movie":
		if rc == nil {
			return mutationNotStarted("Radarr is not configured")
		}
		if err := rc.RemoveQueueItem(queueID, true, blocklist, false, false); err != nil {
			return "", err
		}
	case "tv":
		if sc == nil {
			return mutationNotStarted("Sonarr is not configured")
		}
		if err := sc.RemoveQueueItem(queueID, true, blocklist, false, false); err != nil {
			return "", err
		}
	case "book":
		if cc == nil {
			return mutationNotStarted("Chaptarr is not configured")
		}
		if err := cc.RemoveQueueItem(queueID, true, blocklist, false, false); err != nil {
			return "", err
		}
	default:
		return mutationNotStarted("media_type must be \"movie\", \"tv\", or \"book\"")
	}
	text := fmt.Sprintf("Removed queue item %d and deleted the download from the client.", queueID)
	if blocklist {
		text += " The release was blocklisted so it will not be grabbed again."
	}
	return text, nil
}

// RemediateQueueItemHelper applies one of the structured queue remediations
// (remove | blocklist_search | change_category) to a queue item. Shared body of
// the remediate_queue_item tool and the Executor's remediate_queue kind.
func RemediateQueueItemHelper(rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client, mediaType string, queueID int, action string) (string, error) {
	switch action {
	case "remove", "blocklist_search", "change_category":
	default:
		return mutationNotStarted("action must be \"remove\", \"blocklist_search\", or \"change_category\"")
	}

	switch mediaType {
	case "movie":
		if rc == nil {
			return mutationNotStarted("Radarr is not configured")
		}
		item, err := findRadarrQueueItem(rc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return mutationNotStarted(fmt.Sprintf("no movie queue item with id %d", queueID))
		}
		switch action {
		case "remove":
			if err := rc.RemoveQueueItem(queueID, true, false, false, false); err != nil {
				return "", err
			}
			return fmt.Sprintf("Removed queue item %d (%s) and deleted the download.", queueID, radarrQueueTitle(*item)), nil
		case "blocklist_search":
			if err := rc.RemoveQueueItem(queueID, true, true, false, false); err != nil {
				return "", err
			}
			if item.MovieID != 0 {
				if err := rc.TriggerMoviesSearch([]int{item.MovieID}); err != nil {
					return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed and blocklisted", queueID), Pending: "starting the replacement search", Err: err}
				}
				return fmt.Sprintf("Removed and blocklisted queue item %d (%s) and started a fresh search for a different release.", queueID, radarrQueueTitle(*item)), nil
			}
			return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed and blocklisted", queueID), Pending: "starting the replacement search", Err: fmt.Errorf("the queue item had no movie id")}
		case "change_category":
			if err := rc.RemoveQueueItem(queueID, false, false, false, true); err != nil {
				return "", err
			}
			return fmt.Sprintf("Handed queue item %d (%s) to the download client's post-import category. It stays in the client for tools like Unpackerr.", queueID, radarrQueueTitle(*item)), nil
		}

	case "tv":
		if sc == nil {
			return mutationNotStarted("Sonarr is not configured")
		}
		item, err := findSonarrQueueItem(sc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return mutationNotStarted(fmt.Sprintf("no TV queue item with id %d", queueID))
		}
		switch action {
		case "remove":
			if err := sc.RemoveQueueItem(queueID, true, false, false, false); err != nil {
				return "", err
			}
			return fmt.Sprintf("Removed queue item %d (%s) and deleted the download.", queueID, sonarrQueueTitle(*item)), nil
		case "blocklist_search":
			if err := sc.RemoveQueueItem(queueID, true, true, false, false); err != nil {
				return "", err
			}
			if item.EpisodeID != 0 {
				if err := sc.TriggerEpisodeSearch([]int{item.EpisodeID}); err != nil {
					return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed and blocklisted", queueID), Pending: "starting the replacement search", Err: err}
				}
				return fmt.Sprintf("Removed and blocklisted queue item %d (%s) and started a fresh search for a different release.", queueID, sonarrQueueTitle(*item)), nil
			}
			return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed and blocklisted", queueID), Pending: "starting the replacement search", Err: fmt.Errorf("the queue item had no episode id")}
		case "change_category":
			if err := sc.RemoveQueueItem(queueID, false, false, false, true); err != nil {
				return "", err
			}
			return fmt.Sprintf("Handed queue item %d (%s) to the download client's post-import category. It stays in the client for tools like Unpackerr.", queueID, sonarrQueueTitle(*item)), nil
		}

	case "book":
		if cc == nil {
			return mutationNotStarted("Chaptarr is not configured")
		}
		item, err := findChaptarrQueueItem(cc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return mutationNotStarted(fmt.Sprintf("no book queue item with id %d", queueID))
		}
		switch action {
		case "remove":
			if err := cc.RemoveQueueItem(queueID, true, false, false, false); err != nil {
				return "", err
			}
			return fmt.Sprintf("Removed queue item %d (%s) and deleted the download.", queueID, chaptarrQueueTitle(*item)), nil
		case "blocklist_search":
			if err := cc.RemoveQueueItem(queueID, true, true, false, false); err != nil {
				return "", err
			}
			if item.BookID != 0 {
				if err := cc.TriggerBookSearch([]int{item.BookID}); err != nil {
					return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed and blocklisted", queueID), Pending: "starting the replacement search", Err: err}
				}
				return fmt.Sprintf("Removed and blocklisted queue item %d (%s) and started a fresh search for a different release.", queueID, chaptarrQueueTitle(*item)), nil
			}
			return "", &PartialMutationError{Completed: fmt.Sprintf("queue item %d was removed and blocklisted", queueID), Pending: "starting the replacement search", Err: fmt.Errorf("the queue item had no book id")}
		case "change_category":
			if err := cc.RemoveQueueItem(queueID, false, false, false, true); err != nil {
				return "", err
			}
			return fmt.Sprintf("Handed queue item %d (%s) to the download client's post-import category. It stays in the client for tools like Unpackerr.", queueID, chaptarrQueueTitle(*item)), nil
		}

	default:
		return mutationNotStarted("media_type must be \"movie\", \"tv\", or \"book\"")
	}
	return mutationNotStarted("no remediation was applied")
}

// ExecuteManualImportHelper imports the manual-import candidates for a queue
// item's download, honoring force (force imports despite permanent rejections).
// Shared body of the execute_manual_import tool and the Executor's manual_import
// kind.
type ManualImportScope struct {
	DownloadID    string
	MovieID       int
	SeriesID      int
	BookID        int
	SeasonNumber  int
	EpisodeNumber int
}

type ManualImportScopeError struct{ Detail string }

func (e *ManualImportScopeError) Error() string            { return e.Detail }
func (e *ManualImportScopeError) MutationNotStarted() bool { return true }

func ExecuteManualImportHelper(rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client, mediaType string, queueID int, force bool, scope *ManualImportScope) (string, error) {
	switch mediaType {
	case "movie":
		if rc == nil {
			return mutationNotStarted("Radarr is not configured")
		}
		item, err := findRadarrQueueItem(rc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return mutationNotStarted(fmt.Sprintf("no movie queue item with id %d", queueID))
		}
		if item.DownloadID == "" {
			return mutationNotStarted(fmt.Sprintf("queue item %d has no download-client id yet; nothing to import", queueID))
		}
		if scope != nil && scope.DownloadID != "" && item.DownloadID != scope.DownloadID {
			return "", &ManualImportScopeError{Detail: "the queue item now points to a different download; not importing"}
		}
		candidates, err := rc.GetManualImportCandidates(item.DownloadID)
		if err != nil {
			return "", err
		}
		var files []radarr.ManualImportFile
		var skipped []string
		scopedCandidates := 0
		for _, c := range candidates {
			if scope != nil && (scope.MovieID <= 0 || c.MovieID != scope.MovieID) {
				skipped = append(skipped, fmt.Sprintf("%s (mapped to a different movie)", c.Name))
				continue
			}
			scopedCandidates++
			rejections := toRejectionViews(c.Rejections)
			if !force && hasPermanentRejection(rejections) {
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
			if scope != nil && len(candidates) > 0 && scopedCandidates == 0 {
				return "", &ManualImportScopeError{Detail: "no manual-import candidate maps exclusively to this issue's movie; not importing"}
			}
			return mutationNotStarted(importSkippedMessage(skipped, force))
		}
		importMode := importModeFor(item.Protocol)
		if err := rc.ExecuteManualImport(files, importMode); err != nil {
			return "", err
		}
		return importResultMessage(len(files), importMode, skipped), nil

	case "tv":
		if sc == nil {
			return mutationNotStarted("Sonarr is not configured")
		}
		item, err := findSonarrQueueItem(sc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return mutationNotStarted(fmt.Sprintf("no TV queue item with id %d", queueID))
		}
		if item.DownloadID == "" {
			return mutationNotStarted(fmt.Sprintf("queue item %d has no download-client id yet; nothing to import", queueID))
		}
		if scope != nil && scope.DownloadID != "" && item.DownloadID != scope.DownloadID {
			return "", &ManualImportScopeError{Detail: "the queue item now points to a different download; not importing"}
		}
		candidates, err := sc.GetManualImportCandidates(item.DownloadID)
		if err != nil {
			return "", err
		}
		var files []sonarr.ManualImportFile
		var skipped []string
		scopedCandidates := 0
		for _, c := range candidates {
			if scope != nil && !manualImportCandidateMatchesTVScope(c, *scope) {
				skipped = append(skipped, fmt.Sprintf("%s (mapped outside this issue's series/episode scope)", c.Name))
				continue
			}
			scopedCandidates++
			rejections := toRejectionViews(c.Rejections)
			if !force && hasPermanentRejection(rejections) {
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
			if scope != nil && len(candidates) > 0 && scopedCandidates == 0 {
				return "", &ManualImportScopeError{Detail: "no manual-import candidate maps exclusively to this issue's series/episode; not importing"}
			}
			return mutationNotStarted(importSkippedMessage(skipped, force))
		}
		importMode := importModeFor(item.Protocol)
		if err := sc.ExecuteManualImport(files, importMode); err != nil {
			return "", err
		}
		return importResultMessage(len(files), importMode, skipped), nil

	case "book":
		if cc == nil {
			return mutationNotStarted("Chaptarr is not configured")
		}
		item, err := findChaptarrQueueItem(cc, queueID)
		if err != nil {
			return "", err
		}
		if item == nil {
			return mutationNotStarted(fmt.Sprintf("no book queue item with id %d", queueID))
		}
		if item.DownloadID == "" {
			return mutationNotStarted(fmt.Sprintf("queue item %d has no download-client id yet; nothing to import", queueID))
		}
		if scope != nil && scope.DownloadID != "" && item.DownloadID != scope.DownloadID {
			return "", &ManualImportScopeError{Detail: "the queue item now points to a different download; not importing"}
		}
		candidates, err := cc.GetManualImportCandidates(item.DownloadID)
		if err != nil {
			return "", err
		}
		var files []chaptarr.ManualImportFile
		var skipped []string
		scopedCandidates := 0
		for _, c := range candidates {
			if scope != nil && (scope.BookID <= 0 || c.BookID != scope.BookID) {
				skipped = append(skipped, fmt.Sprintf("%s (mapped to a different book)", c.Name))
				continue
			}
			scopedCandidates++
			rejections := toRejectionViews(c.Rejections)
			if !force && hasPermanentRejection(rejections) {
				skipped = append(skipped, fmt.Sprintf("%s (%s)", c.Name, formatRejections(rejections)))
				continue
			}
			if c.BookID == 0 {
				skipped = append(skipped, fmt.Sprintf("%s (not matched to a book)", c.Name))
				continue
			}
			files = append(files, chaptarr.ManualImportFile{
				Path:         c.Path,
				FolderName:   c.FolderName,
				AuthorID:     c.AuthorID,
				BookID:       c.BookID,
				Quality:      c.Quality,
				ReleaseGroup: c.ReleaseGroup,
				DownloadID:   c.DownloadID,
			})
		}
		if len(files) == 0 {
			if scope != nil && len(candidates) > 0 && scopedCandidates == 0 {
				return "", &ManualImportScopeError{Detail: "no manual-import candidate maps exclusively to this issue's book; not importing"}
			}
			return mutationNotStarted(importSkippedMessage(skipped, force))
		}
		// Chaptarr's ManualImport command sets importMode itself (auto); the
		// helper still reports the protocol-derived mode (copy for torrent, else
		// move) so the result message matches the movie/TV path.
		importMode := importModeFor(item.Protocol)
		if err := cc.ExecuteManualImport(files); err != nil {
			return "", err
		}
		return importResultMessage(len(files), importMode, skipped), nil

	default:
		return mutationNotStarted("media_type must be \"movie\", \"tv\", or \"book\"")
	}
}

func manualImportCandidateMatchesTVScope(candidate sonarr.ManualImportCandidate, scope ManualImportScope) bool {
	if scope.SeriesID <= 0 || candidate.SeriesID != scope.SeriesID || len(candidate.Episodes) == 0 {
		return false
	}
	for _, episode := range candidate.Episodes {
		if episode.ID == 0 {
			return false
		}
		if (scope.SeasonNumber > 0 || scope.EpisodeNumber > 0) && episode.SeasonNumber != scope.SeasonNumber {
			return false
		}
		if scope.EpisodeNumber > 0 && episode.EpisodeNumber != scope.EpisodeNumber {
			return false
		}
	}
	return true
}

// TriggerSearchHelper kicks off an automatic search for a movie, a whole series,
// a single season, or one episode. For books the search targets
// specific bookIDs when present, otherwise every monitored book of authorID;
// books carry no TMDB id, so tmdbID/seasonNumber are unused on the book path
// (and authorID/bookIDs are unused on the movie/TV paths). Shared body of the
// trigger_search tool and the Executor's trigger_search kind.
func TriggerSearchHelper(bridge *tmdb.Bridge, rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client, mediaType string, tmdbID int, seasonNumber, episodeNumber *int, authorID int, bookIDs []int) (string, error) {
	switch mediaType {
	case "movie":
		if rc == nil {
			return mutationNotStarted("Radarr is not configured")
		}
		movie, err := rc.GetMovieByTMDB(tmdbID)
		if err != nil {
			return "", err
		}
		if movie == nil {
			return mutationNotStarted("this movie is not in the library yet")
		}
		if err := rc.TriggerMoviesSearch([]int{movie.ID}); err != nil {
			return "", err
		}
		return fmt.Sprintf("Search started for %s (%d). Check get_queue in a bit to see if a release was grabbed.", movie.Title, movie.Year), nil

	case "tv":
		if sc == nil {
			return mutationNotStarted("Sonarr is not configured")
		}
		series, err := seriesByTMDB(bridge, sc, tmdbID)
		if err != nil {
			return "", err
		}
		if series == nil {
			return mutationNotStarted("this show is not in the library yet")
		}
		if episodeNumber != nil {
			if seasonNumber == nil {
				return mutationNotStarted("season_number is required for an episode search")
			}
			episodes, err := sc.GetEpisodes(series.ID, *seasonNumber)
			if err != nil {
				return "", err
			}
			for _, episode := range episodes {
				if episode.EpisodeNumber != *episodeNumber {
					continue
				}
				if err := sc.TriggerEpisodeSearch([]int{episode.ID}); err != nil {
					return "", err
				}
				return fmt.Sprintf("Search started for %s S%02dE%02d. Check get_queue in a bit to see if a release was grabbed.", series.Title, *seasonNumber, *episodeNumber), nil
			}
			return mutationNotStarted(fmt.Sprintf("episode S%02dE%02d was not found in %s", *seasonNumber, *episodeNumber, series.Title))
		}
		if seasonNumber != nil {
			if err := sc.TriggerSeasonSearch(series.ID, *seasonNumber); err != nil {
				return "", err
			}
			return fmt.Sprintf("Search started for %s season %d. Check get_queue in a bit to see if releases were grabbed.", series.Title, *seasonNumber), nil
		}
		if err := sc.TriggerSeriesSearch(series.ID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Search started for all monitored episodes of %s. Check get_queue in a bit to see if releases were grabbed.", series.Title), nil

	case "book":
		if cc == nil {
			return mutationNotStarted("Chaptarr is not configured")
		}
		if len(bookIDs) > 0 {
			if err := cc.TriggerBookSearch(bookIDs); err != nil {
				return "", err
			}
			return fmt.Sprintf("Search started for %d book(s). Check get_queue in a bit to see if releases were grabbed.", len(bookIDs)), nil
		}
		if authorID == 0 {
			return mutationNotStarted("trigger_search for a book requires author_id or book_id")
		}
		if err := cc.TriggerAuthorSearch(authorID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Search started for all monitored books of author %d. Check get_queue in a bit to see if releases were grabbed.", authorID), nil

	default:
		return mutationNotStarted("media_type must be \"movie\", \"tv\", or \"book\"")
	}
}

// RescanMediaHelper rescans a movie or series on disk and runs the import pass
// (ProcessMonitoredDownloads). For books it rescans authorID (books carry no
// TMDB id, so tmdbID is unused on the book path and authorID is unused on the
// movie/TV paths). Shared body of the rescan_media tool and the Executor's
// rescan kind.
func RescanMediaHelper(bridge *tmdb.Bridge, rc *radarr.Client, sc *sonarr.Client, cc *chaptarr.Client, mediaType string, tmdbID, authorID int) (string, error) {
	switch mediaType {
	case "movie":
		if rc == nil {
			return mutationNotStarted("Radarr is not configured")
		}
		movie, err := rc.GetMovieByTMDB(tmdbID)
		if err != nil {
			return "", err
		}
		if movie == nil {
			return mutationNotStarted("this movie is not in the library")
		}
		if err := rc.RescanMovie(movie.ID); err != nil {
			return "", err
		}
		if err := rc.ProcessMonitoredDownloads(); err != nil {
			return "", &PartialMutationError{Completed: fmt.Sprintf("a rescan of movie %d was started", movie.ID), Pending: "starting the monitored-download import pass", Err: err}
		}
		return fmt.Sprintf("Rescanning %s (%d) and running the import pass. Check diagnose_queue shortly.", movie.Title, movie.Year), nil

	case "tv":
		if sc == nil {
			return mutationNotStarted("Sonarr is not configured")
		}
		series, err := seriesByTMDB(bridge, sc, tmdbID)
		if err != nil {
			return "", err
		}
		if series == nil {
			return mutationNotStarted("this show is not in the library")
		}
		if err := sc.RescanSeries(series.ID); err != nil {
			return "", err
		}
		if err := sc.ProcessMonitoredDownloads(); err != nil {
			return "", &PartialMutationError{Completed: fmt.Sprintf("a rescan of series %d was started", series.ID), Pending: "starting the monitored-download import pass", Err: err}
		}
		return fmt.Sprintf("Rescanning %s and running the import pass. Check diagnose_queue shortly.", series.Title), nil

	case "book":
		if cc == nil {
			return mutationNotStarted("Chaptarr is not configured")
		}
		if authorID == 0 {
			return mutationNotStarted("rescan for a book requires author_id")
		}
		if err := cc.RescanAuthor(authorID); err != nil {
			return "", err
		}
		if err := cc.ProcessMonitoredDownloads(); err != nil {
			return "", &PartialMutationError{Completed: fmt.Sprintf("a rescan of author %d was started", authorID), Pending: "starting the monitored-download import pass", Err: err}
		}
		return fmt.Sprintf("Rescanning author %d and running the import pass. Check diagnose_queue shortly.", authorID), nil

	default:
		return mutationNotStarted("media_type must be \"movie\", \"tv\", or \"book\"")
	}
}
