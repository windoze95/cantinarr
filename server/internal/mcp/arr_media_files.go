package mcp

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/lidarr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

// This file owns the two mutations that act on media the *arr has already
// IMPORTED, rather than on a live queue row: deleting the file it holds, and
// blocklisting the release that delivered it.
//
// Every other fix in the catalog assumes the problem is still in the queue. The
// report this exists for never is — "this is the wrong episode" is only visible
// once the download finished, imported, and the queue went empty, which on a
// pre-air season fill can be weeks earlier.

// Sonarr/Radarr history event types, as the API renders them on a read (a
// string, not the numeric code the eventType QUERY parameter takes).
const (
	historyEventGrabbed  = "grabbed"
	historyEventImported = "downloadFolderImported"
)

// deletedFile is one file removed by DeleteMediaFilesHelper, kept so the outcome
// text can name exactly what changed and the blocklist pass can find the grab
// that delivered it.
type deletedFile struct {
	label   string // "S11E03" or the movie title — for the admin-facing summary
	mediaID int    // Sonarr episode id / Radarr movie id, for the history walk
}

// DeleteMediaFilesHelper removes files the *arr already imported and, when
// blocklist is set, marks the grab that delivered each one as failed.
//
// This is the WHOLE repair, in one admin approval: delete, blocklist, replace.
// One problem gets one decision — an admin who approved "delete the wrong files
// and get the right ones" is not asked back to authorise the second half of
// their own sentence.
//
// Two ordering rules are load-bearing:
//
//   - Files are deleted BEFORE anything is blocklisted, and the replacement
//     search runs LAST. Blocklisting is what triggers the service's own
//     failed-download handling, and a replacement found while the old file is
//     still on disk is rejected as "not an upgrade" — the fix would report
//     success and change nothing.
//   - The repair's own search is called off in exactly one case: blocklisting
//     already made the service go looking. That is the boundary PR #363 drew —
//     never duplicate or overrule the admin's own autoRedownloadFailed setting —
//     and it is a reason to skip a redundant search, not a reason to hand half a
//     repair back to a human. See replaceWhatAired.
//
// A partial failure is reported in the text with a nil error whenever at least
// one file was actually deleted: the Executor treats a non-nil error as a
// definitive failure that mutated nothing, and that would be a lie.
//
// proposedAt is when the fix was proposed, and guards against deleting a file
// that arrived after the diagnosis and might be genuine — see
// replacedSinceDiagnosis.
func DeleteMediaFilesHelper(bridge *tmdb.Bridge, rc *radarr.Client, sc *sonarr.Client, mediaType string, tmdbID int, season *int, episodes []int, blocklist bool, proposedAt time.Time) (string, error) {
	switch mediaType {
	case "tv":
		return deleteSonarrEpisodeFiles(bridge, sc, tmdbID, season, episodes, blocklist, proposedAt)
	case "movie":
		return deleteRadarrMovieFile(rc, tmdbID, blocklist, proposedAt)
	default:
		return mutationNotStarted("delete_media_files supports media_type \"movie\" or \"tv\"")
	}
}

// diagnosisClockSlack absorbs the difference between Cantinarr's clock, which
// stamps the proposal, and the *arr's, which stamps the import. A few seconds
// either way must not read as a replaced file.
const diagnosisClockSlack = 2 * time.Minute

// replacedSinceDiagnosis reports that the file sitting here now might be a
// genuine one that arrived after the fix naming it was proposed — in which case
// deleting it would destroy something nobody looked at.
//
// The window this closes is real and gets worse the better the fix works. A
// proposal waits on a human, and the *arr keeps working the whole time: once the
// episode actually airs, an upgrade can replace the bogus file with the real
// release. Approving the old proposal would then delete the right file AND
// blocklist the right release — the exact opposite of the repair.
//
// But "newer than the diagnosis" alone is too blunt. A replacement that STILL
// landed before its episode aired is exactly as impossible as the one it
// replaced, and refusing to delete it would leave the season broken and make the
// agent re-propose the identical fix. So the question is not whether the file
// changed, it is whether the file could be real: arrived after the diagnosis AND
// after the episode existed to be recorded.
//
// airsAt may be nil where there is no air date to reason about — a movie, an
// unscheduled episode — and then any newer file is protected.
//
// Fails open on a missing import stamp. This gate exists to spare a file that
// might be genuine, not to demand proof that a file is fake; an *arr that omits
// the field must not turn an approved repair into a silent no-op.
func replacedSinceDiagnosis(importedAt, airsAt *time.Time, proposedAt time.Time) bool {
	if importedAt == nil || proposedAt.IsZero() {
		return false
	}
	if !importedAt.After(proposedAt.Add(diagnosisClockSlack)) {
		return false
	}
	return airsAt == nil || !importedAt.Before(*airsAt)
}

// DeleteBookFilesHelper is the wrong-book repair: delete the imported file(s)
// for one Chaptarr book record and, with blocklist, mark the grabs that
// delivered them failed so they cannot come back. Books have no air dates, so
// there is no aired-only replacement half — the staleness gate spares only
// files that arrived AFTER the fix was proposed (a replacement someone else
// already made), and the replacement search follows the same standdown rule
// as TV/movies: when autoRedownloadFailed is on, marking the grabs failed IS
// the search trigger and Cantinarr adds none of its own.
func DeleteBookFilesHelper(cc *chaptarr.Client, bookID int, blocklist bool, proposedAt time.Time) (string, error) {
	if cc == nil {
		return mutationNotStarted("Chaptarr is not configured")
	}
	if bookID <= 0 {
		return mutationNotStarted("delete_media_files for a book requires the issue's book id")
	}
	files, err := cc.GetBookFilesForBook(bookID)
	if err != nil {
		return "", fmt.Errorf("read book files: %w", err)
	}
	if len(files) == 0 {
		return mutationNotStarted("this book holds no file to delete")
	}
	var deleted, skipped, failures []string
	for _, f := range files {
		label := f.Path
		if label == "" {
			label = fmt.Sprintf("file %d", f.ID)
		}
		if f.DateAdded != nil && f.DateAdded.After(proposedAt.Add(2*time.Minute)) {
			skipped = append(skipped, label+" (arrived after this fix was proposed)")
			continue
		}
		if err := cc.DeleteBookFile(f.ID); err != nil {
			failures = append(failures, fmt.Sprintf("%s (%v)", label, err))
			continue
		}
		deleted = append(deleted, label)
	}
	if len(deleted) == 0 {
		if len(failures) > 0 {
			return "", fmt.Errorf("could not delete any file: %s", strings.Join(failures, "; "))
		}
		return mutationNotStarted("nothing to delete: " + strings.Join(skipped, ", "))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Deleted %d book file(s).", len(deleted))
	serviceWillReplace := false
	if blocklist {
		grabs, gerr := cc.GetBookGrabs(bookID, 50)
		if gerr != nil {
			sb.WriteString(" The releases could NOT be blocklisted (reading history failed); an admin should mark them failed in Chaptarr.")
		} else {
			blocked := 0
			seen := map[string]struct{}{}
			for _, grab := range grabs {
				if grab.DownloadID == "" {
					continue
				}
				if _, dup := seen[grab.DownloadID]; dup {
					continue
				}
				seen[grab.DownloadID] = struct{}{}
				if err := cc.MarkHistoryFailed(int64(grab.ID)); err == nil {
					blocked++
				}
			}
			fmt.Fprintf(&sb, " Blocklisted %d release(s).", blocked)
			if blocked > 0 {
				if auto, perr := cc.GetFailedDownloadPolicy(); perr == nil && auto {
					serviceWillReplace = true
					sb.WriteString(" Chaptarr's failed-download handling is on, so it searches for the replacement itself.")
				}
			}
		}
	}
	if !serviceWillReplace {
		if err := cc.TriggerBookSearch([]int{bookID}); err == nil {
			sb.WriteString(" Started a search for a replacement.")
		} else {
			sb.WriteString(" A replacement search could not be started; monitor the book or search manually.")
		}
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&sb, " Skipped: %s.", strings.Join(skipped, ", "))
	}
	return sb.String(), nil
}

// DeleteTrackFilesHelper is the wrong-album repair: delete the imported file(s)
// for one Lidarr album record and, with blocklist, mark the grabs that
// delivered them failed so they cannot come back. Albums have no air dates, so
// there is no aired-only replacement half — the staleness gate spares only
// files that arrived AFTER the fix was proposed (a replacement someone else
// already made), and the replacement search follows the same standdown rule
// as TV/movies: when autoRedownloadFailed is on, marking the grabs failed IS
// the search trigger and Cantinarr adds none of its own.
func DeleteTrackFilesHelper(lc *lidarr.Client, albumID int, blocklist bool, proposedAt time.Time) (string, error) {
	if lc == nil {
		return mutationNotStarted("Lidarr is not configured")
	}
	if albumID <= 0 {
		return mutationNotStarted("delete_media_files for music requires the issue's album id")
	}
	files, err := lc.GetTrackFilesForAlbum(albumID)
	if err != nil {
		return "", fmt.Errorf("read album track files: %w", err)
	}
	if len(files) == 0 {
		return mutationNotStarted("this album holds no file to delete")
	}
	var deleted, skipped, failures []string
	for _, f := range files {
		label := f.Path
		if label == "" {
			label = fmt.Sprintf("file %d", f.ID)
		}
		if f.DateAdded != nil && f.DateAdded.After(proposedAt.Add(2*time.Minute)) {
			skipped = append(skipped, label+" (arrived after this fix was proposed)")
			continue
		}
		if err := lc.DeleteTrackFile(f.ID); err != nil {
			failures = append(failures, fmt.Sprintf("%s (%v)", label, err))
			continue
		}
		deleted = append(deleted, label)
	}
	if len(deleted) == 0 {
		if len(failures) > 0 {
			return "", fmt.Errorf("could not delete any file: %s", strings.Join(failures, "; "))
		}
		return mutationNotStarted("nothing to delete: " + strings.Join(skipped, ", "))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Deleted %d track file(s).", len(deleted))
	serviceWillReplace := false
	if blocklist {
		grabs, gerr := lc.GetAlbumGrabs(albumID, 50)
		if gerr != nil {
			sb.WriteString(" The releases could NOT be blocklisted (reading history failed); an admin should mark them failed in Lidarr.")
		} else {
			blocked := 0
			seen := map[string]struct{}{}
			for _, grab := range grabs {
				if grab.DownloadID == "" {
					continue
				}
				if _, dup := seen[grab.DownloadID]; dup {
					continue
				}
				seen[grab.DownloadID] = struct{}{}
				if err := lc.MarkHistoryFailed(int64(grab.ID)); err == nil {
					blocked++
				}
			}
			fmt.Fprintf(&sb, " Blocklisted %d release(s).", blocked)
			if blocked > 0 {
				if auto, perr := lc.GetFailedDownloadPolicy(); perr == nil && auto {
					serviceWillReplace = true
					sb.WriteString(" Lidarr's failed-download handling is on, so it searches for the replacement itself.")
				}
			}
		}
	}
	if !serviceWillReplace {
		if err := lc.TriggerAlbumSearch([]int{albumID}); err == nil {
			sb.WriteString(" Started a search for a replacement.")
		} else {
			sb.WriteString(" A replacement search could not be started; monitor the album or search manually.")
		}
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&sb, " Skipped: %s.", strings.Join(skipped, ", "))
	}
	return sb.String(), nil
}

func deleteSonarrEpisodeFiles(bridge *tmdb.Bridge, sc *sonarr.Client, tmdbID int, season *int, episodes []int, blocklist bool, proposedAt time.Time) (string, error) {
	if sc == nil {
		return mutationNotStarted("Sonarr is not configured")
	}
	if season == nil {
		return mutationNotStarted("delete_media_files for TV requires a season")
	}
	series, err := seriesByTMDB(bridge, sc, tmdbID)
	if err != nil {
		return "", err
	}
	if series == nil {
		return mutationNotStarted("this show is not in the library yet")
	}
	all, err := sc.GetEpisodes(series.ID, *season)
	if err != nil {
		return "", err
	}
	byNumber := make(map[int]sonarr.Episode, len(all))
	for _, ep := range all {
		byNumber[ep.EpisodeNumber] = ep
	}
	// The episode record knows which file it holds; only the file record knows
	// when that file arrived, which is what the staleness gate turns on.
	files, err := sc.GetEpisodeFiles(series.ID)
	if err != nil {
		return "", err
	}
	importedAt := make(map[int]*time.Time, len(files))
	for _, f := range files {
		importedAt[f.ID] = f.DateAdded
	}

	var (
		deleted  []deletedFile
		skipped  []string
		failures []string
	)
	for _, number := range episodes {
		label := fmt.Sprintf("S%02dE%02d", *season, number)
		ep, ok := byNumber[number]
		if !ok {
			skipped = append(skipped, label+" (not in this season)")
			continue
		}
		if !ep.HasFile || ep.EpisodeFileID <= 0 {
			skipped = append(skipped, label+" (no file)")
			continue
		}
		if replacedSinceDiagnosis(importedAt[ep.EpisodeFileID], ep.AirDateUtc, proposedAt) {
			skipped = append(skipped, label+" (a different file arrived after this fix was proposed)")
			continue
		}
		if err := sc.DeleteEpisodeFile(ep.EpisodeFileID); err != nil {
			failures = append(failures, fmt.Sprintf("%s (%v)", label, err))
			continue
		}
		deleted = append(deleted, deletedFile{label: label, mediaID: ep.ID})
	}

	if len(deleted) == 0 {
		if len(failures) > 0 {
			return "", fmt.Errorf("could not delete any file: %s", strings.Join(failures, "; "))
		}
		return mutationNotStarted(fmt.Sprintf("nothing to delete for %s season %d: %s",
			series.Title, *season, strings.Join(skipped, ", ")))
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Deleted %s from %s season %d.", pluralFiles(len(deleted)), series.Title, *season)
	fmt.Fprintf(&sb, " Episodes: %s.", joinLabels(deleted))

	serviceWillReplace := false
	if blocklist {
		history, herr := sc.GetSeriesHistory(series.ID, *season, 0)
		if herr != nil {
			fmt.Fprintf(&sb, " The releases could NOT be blocklisted (reading history failed: %v); an admin should mark them failed in Sonarr.", herr)
		} else {
			text, blocked := blocklistOutcome(sc.MarkHistoryFailed, sonarrGrabsFor(history, deleted))
			sb.WriteString(text)
			serviceWillReplace = appendReplacementPolicy(&sb, blocked, sc.GetFailedDownloadPolicy)
		}
	}
	sb.WriteString(replaceWhatAired(sc, series, *season, serviceWillReplace))

	if len(skipped) > 0 {
		fmt.Fprintf(&sb, " Left alone: %s.", strings.Join(skipped, ", "))
	}
	if len(failures) > 0 {
		fmt.Fprintf(&sb, " Could not delete: %s.", strings.Join(failures, "; "))
	}
	return sb.String(), nil
}

func deleteRadarrMovieFile(rc *radarr.Client, tmdbID int, blocklist bool, proposedAt time.Time) (string, error) {
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
	fileID := movie.MovieFileID
	if fileID <= 0 {
		fileID = movie.MovieFile.ID
	}
	if fileID <= 0 {
		return mutationNotStarted(fmt.Sprintf("%s has no file to delete", movie.Title))
	}
	if file, ferr := rc.GetMovieFile(fileID); ferr == nil && file != nil &&
		replacedSinceDiagnosis(file.DateAdded, nil, proposedAt) {
		return mutationNotStarted(fmt.Sprintf(
			"%s now holds a different file than the one this fix was proposed for; nothing was deleted", movie.Title))
	}
	if err := rc.DeleteMovieFile(fileID); err != nil {
		return "", err
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Deleted the imported file for %s.", movie.Title)
	serviceWillReplace := false
	if blocklist {
		history, herr := rc.GetMovieHistory(movie.ID, 0)
		if herr != nil {
			fmt.Fprintf(&sb, " The release could NOT be blocklisted (reading history failed: %v); an admin should mark it failed in Radarr.", herr)
		} else {
			text, blocked := blocklistOutcome(rc.MarkHistoryFailed, radarrGrabsFor(history, movie.ID))
			sb.WriteString(text)
			serviceWillReplace = appendReplacementPolicy(&sb, blocked, rc.GetFailedDownloadPolicy)
		}
	}
	if serviceWillReplace {
		return sb.String(), nil
	}
	if err := rc.TriggerMoviesSearch([]int{movie.ID}); err != nil {
		fmt.Fprintf(&sb, " The replacement search could not be started (%v); an admin should search for it in Radarr.", err)
		return sb.String(), nil
	}
	sb.WriteString(" Searched for a replacement.")
	return sb.String(), nil
}

// grabToFail is one release to mark failed: the history row that recorded the
// grab, plus the title an admin will recognise it by.
type grabToFail struct {
	historyID int64
	title     string
}

// sonarrGrabsFor walks a series' history back from each deleted file to the grab
// that delivered it: newest import for that episode → its download id → newest
// grab carrying the same download id.
//
// Going through the download id rather than the episode is what makes a real
// season pack work. One pack is one download that imported as ten episodes, so
// ten deletions must still blocklist exactly one release — deduping by download
// id collapses them. (The production case that prompted this went the other way:
// nine separate RSS grabs, nine download ids, nine releases to stand down.)
func sonarrGrabsFor(history []sonarr.HistoryRecord, deleted []deletedFile) []grabToFail {
	imports := make(map[int]string, len(deleted))
	wanted := make(map[int]struct{}, len(deleted))
	for _, d := range deleted {
		wanted[d.mediaID] = struct{}{}
	}
	// History arrives newest-first, so the first record seen for an episode is
	// the import that produced the file that was just deleted.
	for _, rec := range history {
		if rec.EventType != historyEventImported || rec.DownloadID == "" {
			continue
		}
		if _, want := wanted[rec.EpisodeID]; !want {
			continue
		}
		if _, seen := imports[rec.EpisodeID]; !seen {
			imports[rec.EpisodeID] = rec.DownloadID
		}
	}
	picked := make(map[string]grabToFail, len(imports))
	for _, rec := range history {
		if rec.EventType != historyEventGrabbed || rec.DownloadID == "" {
			continue
		}
		if _, seen := picked[rec.DownloadID]; seen {
			continue
		}
		for _, downloadID := range imports {
			if downloadID == rec.DownloadID {
				picked[rec.DownloadID] = grabToFail{historyID: rec.ID, title: rec.SourceTitle}
				break
			}
		}
	}
	return sortedGrabs(picked)
}

// radarrGrabsFor is the movie equivalent: the newest import for this movie names
// the download id, and the newest grab carrying that id is the release to fail.
func radarrGrabsFor(history []radarr.HistoryRecord, movieID int) []grabToFail {
	downloadID := ""
	for _, rec := range history {
		if rec.EventType == historyEventImported && rec.DownloadID != "" && rec.MovieID == movieID {
			downloadID = rec.DownloadID
			break
		}
	}
	if downloadID == "" {
		return nil
	}
	picked := make(map[string]grabToFail, 1)
	for _, rec := range history {
		if rec.EventType != historyEventGrabbed || rec.DownloadID != downloadID {
			continue
		}
		picked[rec.DownloadID] = grabToFail{historyID: rec.ID, title: rec.SourceTitle}
		break
	}
	return sortedGrabs(picked)
}

// sortedGrabs flattens the per-download picks into a stable order so the outcome
// text — and any test reading it — is deterministic.
func sortedGrabs(picked map[string]grabToFail) []grabToFail {
	out := make([]grabToFail, 0, len(picked))
	for _, g := range picked {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].historyID < out[j].historyID })
	return out
}

// blocklistOutcome marks each grab failed and renders one sentence about it.
// A blocklist failure never fails the whole action: the files are already gone,
// which is the irreversible half, and the admin needs to be told what is left to
// do rather than shown a failed fix that half-happened.
func blocklistOutcome(markFailed func(int64) error, grabs []grabToFail) (string, bool) {
	if len(grabs) == 0 {
		return " No grab record was found for those files, so nothing could be blocklisted — the service has no record of where they came from.", false
	}
	var blocked, failed []string
	for _, g := range grabs {
		if err := markFailed(g.historyID); err != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", releaseLabel(g.title), err))
			continue
		}
		blocked = append(blocked, releaseLabel(g.title))
	}
	var sb strings.Builder
	if len(blocked) > 0 {
		fmt.Fprintf(&sb, " Blocklisted %d release(s): %s.", len(blocked), strings.Join(blocked, ", "))
	}
	if len(failed) > 0 {
		fmt.Fprintf(&sb, " Could not blocklist: %s.", strings.Join(failed, "; "))
	}
	return sb.String(), len(blocked) > 0
}

// appendReplacementPolicy states which way the instance's own failed-download
// setting is pointing. Cantinarr deliberately does not search after a blocklist
// (PR #363) — so when the setting is off, the admin is the one who has to decide
// on a replacement, and saying nothing would leave a gap they never see.
//
// Only says anything when a release was actually stood down. The setting governs
// what a FAILED grab does, so quoting it after a blocklist that did not happen
// would promise a replacement search that nothing will trigger.
func appendReplacementPolicy(sb *strings.Builder, blocklisted bool, policy func() (bool, error)) bool {
	if !blocklisted || policy == nil {
		return false
	}
	auto, err := policy()
	if err != nil {
		// Unknown policy: say nothing and let the repair finish the job itself.
		// A duplicate search costs a wasted indexer query; a skipped one leaves
		// the reporter with the missing episode they complained about.
		return false
	}
	if auto {
		sb.WriteString(" This service is set to redownload failed grabs, so it is looking for replacements itself.")
		return true
	}
	sb.WriteString(" This service does not redownload failed grabs on its own.")
	return false
}

// releaseLabel bounds an arr-supplied release title for an admin-facing summary.
func releaseLabel(title string) string {
	const max = 70
	title = strings.TrimSpace(title)
	if title == "" {
		return "(untitled release)"
	}
	if len(title) > max {
		return title[:max] + "…"
	}
	return title
}

func joinLabels(files []deletedFile) string {
	labels := make([]string, 0, len(files))
	for _, f := range files {
		labels = append(labels, f.label)
	}
	return strings.Join(labels, ", ")
}

func pluralFiles(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// replaceWhatAired finishes the repair: the episodes that have aired and are now
// missing get searched, and the rest of the season is left for the service to
// grab as it comes out.
//
// This is part of the SAME fix, not a follow-up. Deleting the wrong files and
// not replacing them is half a repair, and an admin who approved "delete the
// wrong files and get the right ones" should not have to come back and approve
// the second half of their own decision.
//
// serviceWillReplace is the one thing that can call it off. Marking a grab
// failed is what triggers the service's own failed-download handling, so when
// that is switched on the search has already been dispatched by the service and
// ours would only duplicate it — the rule PR #363 settled. When it is off, or
// when nothing was blocklisted to trigger it, the search belongs to the repair.
func replaceWhatAired(sc *sonarr.Client, series *sonarr.Series, seasonNumber int, serviceWillReplace bool) string {
	if serviceWillReplace {
		return ""
	}
	// A failed search never fails the repair. The files are already gone, which
	// is the irreversible half; the admin needs to be told what is left to do
	// rather than shown a half-happened fix reported as a clean failure.
	text, err := triggerAiredEpisodeSearch(sc, series, seasonNumber)
	if err != nil {
		return fmt.Sprintf(" The replacement search could not be started (%v); an admin should search the aired episodes in Sonarr.", err)
	}
	return " " + text
}

// triggerAiredEpisodeSearch searches exactly the episodes of a season that have
// aired and have no file. Episodes that have not aired are deliberately left
// alone: there is nothing legitimate to find yet, and searching for them is how
// a library ends up holding a whole season that does not exist.
func triggerAiredEpisodeSearch(sc *sonarr.Client, series *sonarr.Series, seasonNumber int) (string, error) {
	episodes, err := sc.GetEpisodes(series.ID, seasonNumber)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	var (
		ids       []int
		labels    []string
		airedHeld int
		unaired   int
	)
	for _, ep := range episodes {
		if ep.AirDateUtc == nil {
			continue
		}
		if ep.AirDateUtc.After(now) {
			unaired++
			continue
		}
		if ep.HasFile {
			airedHeld++
			continue
		}
		ids = append(ids, ep.ID)
		labels = append(labels, fmt.Sprintf("E%02d", ep.EpisodeNumber))
	}
	if len(ids) == 0 {
		// An empty set is the correct answer to "search whatever has aired", not
		// a precondition failure — so this is a completed action, not a refused
		// one. But WHY it is empty matters: no episode out yet is a healthy
		// answer, while every aired episode still holding a file usually means
		// the files this was meant to follow are still there.
		if airedHeld > 0 {
			return fmt.Sprintf("Nothing to search: all %d aired episode(s) of %s season %d still have a file. If those files are the wrong ones, they have to be deleted first.",
				airedHeld, series.Title, seasonNumber), nil
		}
		return fmt.Sprintf("Nothing to search: no episode of %s season %d has aired yet. The service will grab each one as it airs.",
			series.Title, seasonNumber), nil
	}
	if err := sc.TriggerEpisodeSearch(ids); err != nil {
		return "", err
	}
	out := fmt.Sprintf("Searched the %d aired episode(s) of %s season %d that are missing a file: %s.",
		len(ids), series.Title, seasonNumber, strings.Join(labels, ", "))
	if unaired > 0 {
		out += fmt.Sprintf(" The %d episode(s) still to air were left alone — the service will grab each one as it comes out.", unaired)
	}
	return out, nil
}
