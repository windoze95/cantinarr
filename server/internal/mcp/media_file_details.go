package mcp

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/lidarr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// get_media_file_details is the agent's eyes INSIDE a file it is being asked
// to judge. "Wrong audio", "no subtitles", and "bad copy" were structurally
// unfalsifiable before this read: nothing anywhere surfaced the arr's own
// ffprobe truth, so the agent could only pattern-match a release-name string.
// This is a per-title API read of what the arr already analyzed — nothing
// scans the library, and nothing in this server ever does.

type mediaFileDetailsArgs struct {
	MediaType    string `json:"media_type"`
	InstanceID   string `json:"instance_id"`
	TmdbID       int    `json:"tmdb_id"`
	SeasonNumber int    `json:"season_number"`
	AlbumID      int    `json:"album_id"`
}

func mediaFileDetailsInput(input json.RawMessage) (mediaFileDetailsArgs, error) {
	var args mediaFileDetailsArgs
	if err := json.Unmarshal(nonEmptyJSON(input), &args); err != nil {
		return mediaFileDetailsArgs{}, fmt.Errorf("invalid get_media_file_details input: %w", err)
	}
	return args, nil
}

func (s *ToolServer) getMediaFileDetails(input json.RawMessage, callInstanceID string) (*ToolResult, error) {
	args, err := mediaFileDetailsInput(input)
	if err != nil {
		return nil, err
	}
	instanceID := arrToolInstanceID(args.InstanceID, callInstanceID)
	switch args.MediaType {
	case "movie":
		if args.TmdbID <= 0 {
			return &ToolResult{Text: "Movie file details need the movie's positive tmdb_id."}, nil
		}
		if client, _, refusal := s.radarrTargetFor(args.InstanceID, callInstanceID); client == nil {
			return &ToolResult{Text: refusal}, nil
		}
		return s.movieFileDetails(args.TmdbID, instanceID)
	case "tv":
		if args.TmdbID <= 0 {
			return &ToolResult{Text: "TV file details need the series' positive tmdb_id (and the season_number to inspect)."}, nil
		}
		if client, _, refusal := s.sonarrTargetFor(args.InstanceID, callInstanceID); client == nil {
			return &ToolResult{Text: refusal}, nil
		}
		return s.episodeFileDetails(args.TmdbID, args.SeasonNumber, instanceID)
	case "music":
		if args.AlbumID <= 0 {
			return &ToolResult{Text: "Album file details need the album's positive Lidarr album_id (music has no tmdb_id)."}, nil
		}
		if client, _, refusal := s.lidarrTargetFor(args.InstanceID, callInstanceID); client == nil {
			return &ToolResult{Text: refusal}, nil
		}
		return s.albumFileDetails(args.AlbumID, instanceID)
	default:
		return &ToolResult{Text: "get_media_file_details reads movie, tv, and music files. Book files carry no media-property analysis in Chaptarr; this read cannot see them — that is blindness, not absence."}, nil
	}
}

func describeMediaInfo(res, videoCodec, dynamicRange, audioCodec string, channels float64, audioLangs, subs, runTime string) string {
	var sb strings.Builder
	if res != "" {
		fmt.Fprintf(&sb, "%s %s", res, videoCodec)
		if dynamicRange != "" {
			fmt.Fprintf(&sb, " %s", dynamicRange)
		}
	} else {
		sb.WriteString("video properties unknown")
	}
	if audioCodec != "" || audioLangs != "" {
		fmt.Fprintf(&sb, " · audio %s %.1fch", audioCodec, channels)
		if audioLangs != "" {
			fmt.Fprintf(&sb, " [%s]", audioLangs)
		}
	}
	if subs != "" {
		fmt.Fprintf(&sb, " · subs [%s]", subs)
	} else {
		sb.WriteString(" · no embedded subtitles listed")
	}
	if runTime != "" {
		fmt.Fprintf(&sb, " · runtime %s", runTime)
	}
	return sb.String()
}

func (s *ToolServer) movieFileDetails(tmdbID int, instanceID string) (*ToolResult, error) {
	client := s.GetRadarrFor(instanceID)
	if client == nil {
		return &ToolResult{Text: "Radarr is not configured."}, nil
	}
	movie, err := client.GetMovieByTMDB(tmdbID)
	if err != nil {
		return nil, fmt.Errorf("resolve movie: %w", err)
	}
	if movie == nil {
		return &ToolResult{Text: "This movie is not in the library, so there is no file to inspect. This read checked the exact TMDB id against the live Radarr library."}, nil
	}
	fileID := movie.MovieFileID
	if fileID == 0 {
		fileID = movie.MovieFile.ID
	}
	if fileID <= 0 {
		return &ToolResult{Text: fmt.Sprintf("%s is in the library but holds NO file right now. This read checked the movie's own file linkage; an empty answer here means the file is genuinely absent, not unseen.", movie.Title)}, nil
	}
	file, err := client.GetMovieFile(fileID)
	if err != nil {
		return nil, fmt.Errorf("read movie file: %w", err)
	}
	if file == nil {
		return &ToolResult{Text: fmt.Sprintf("%s links file id %d but Radarr no longer serves that record. This read asked for the exact file.", movie.Title, fileID)}, nil
	}
	files := []radarr.MovieFile{*file}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — %d file(s):\n", movie.Title, len(files))
	for _, f := range files {
		fmt.Fprintf(&sb, "- %s (%.1f GB", f.RelativePath, float64(f.Size)/1e9)
		if f.Quality.Quality.Name != "" {
			fmt.Fprintf(&sb, ", %s", f.Quality.Quality.Name)
		}
		if f.DateAdded != nil {
			fmt.Fprintf(&sb, ", imported %s", f.DateAdded.UTC().Format("2006-01-02"))
		}
		sb.WriteString(")\n")
		sb.WriteString("  ")
		sb.WriteString(movieMediaInfoLine(f))
		sb.WriteString("\n")
	}
	return &ToolResult{Text: sb.String()}, nil
}

func movieMediaInfoLine(f radarr.MovieFile) string {
	if f.MediaInfo == nil {
		return "Radarr has not analyzed this file's media properties yet — audio/subtitle/resolution truth is UNKNOWN here (blindness, not absence)."
	}
	m := f.MediaInfo
	return describeMediaInfo(m.Resolution, m.VideoCodec, m.VideoDynamicRange, m.AudioCodec, m.AudioChannels, m.AudioLanguages, m.Subtitles, m.RunTime)
}

func (s *ToolServer) episodeFileDetails(tmdbID, season int, instanceID string) (*ToolResult, error) {
	client := s.GetSonarrFor(instanceID)
	if client == nil {
		return &ToolResult{Text: "Sonarr is not configured."}, nil
	}
	series := resolveScopedSeries(s.bridge, client, mediaReadScope{TmdbID: tmdbID})
	if series == nil {
		return &ToolResult{Text: "This series is not in the library, so there are no files to inspect. This read checked the exact id against the live Sonarr library."}, nil
	}
	files, err := client.GetEpisodeFiles(series.ID)
	if err != nil {
		return nil, fmt.Errorf("read episode files: %w", err)
	}
	byID := make(map[int]sonarr.EpisodeFile, len(files))
	for _, f := range files {
		byID[f.ID] = f
	}
	episodes, err := client.GetEpisodes(series.ID, season)
	if err != nil {
		return nil, fmt.Errorf("read episodes: %w", err)
	}
	var sb strings.Builder
	shown := 0
	fmt.Fprintf(&sb, "%s season %d files:\n", series.Title, season)
	for _, ep := range episodes {
		if !ep.HasFile || ep.EpisodeFileID <= 0 {
			continue
		}
		f, ok := byID[ep.EpisodeFileID]
		if !ok {
			continue
		}
		shown++
		fmt.Fprintf(&sb, "- S%02dE%02d %s (%.1f GB", season, ep.EpisodeNumber, f.SceneName, float64(f.Size)/1e9)
		if f.Quality.Quality.Name != "" {
			fmt.Fprintf(&sb, ", %s", f.Quality.Quality.Name)
		}
		if f.DateAdded != nil {
			fmt.Fprintf(&sb, ", imported %s", f.DateAdded.UTC().Format("2006-01-02"))
		}
		sb.WriteString(")\n  ")
		if f.MediaInfo == nil {
			sb.WriteString("Sonarr has not analyzed this file's media properties yet — audio/subtitle/resolution truth is UNKNOWN here (blindness, not absence).")
		} else {
			m := f.MediaInfo
			sb.WriteString(describeMediaInfo(m.Resolution, m.VideoCodec, m.VideoDynamicRange, m.AudioCodec, m.AudioChannels, m.AudioLanguages, m.Subtitles, m.RunTime))
		}
		sb.WriteString("\n")
	}
	if shown == 0 {
		fmt.Fprintf(&sb, "No episode in season %d holds a file right now. This read listed the season's own episode and file records; an empty answer here is genuine absence.\n", season)
	}
	return &ToolResult{Text: sb.String()}, nil
}

// albumFileDetails is the music arm: Lidarr analyzes audio the way Radarr and
// Sonarr analyze video, so a "bad copy" or "wrong quality" album report can be
// judged on the files rather than on a release name.
func (s *ToolServer) albumFileDetails(albumID int, instanceID string) (*ToolResult, error) {
	client := s.GetLidarrFor(instanceID)
	if client == nil {
		return &ToolResult{Text: "Lidarr is not configured."}, nil
	}
	album, err := client.GetAlbum(albumID)
	if err != nil {
		return nil, fmt.Errorf("resolve album: %w", err)
	}
	if album == nil {
		return &ToolResult{Text: "This album is not in the library, so there are no files to inspect. This read checked the exact album id against the live Lidarr library."}, nil
	}
	files, err := client.GetTrackFilesForAlbum(albumID)
	if err != nil {
		return nil, fmt.Errorf("read track files: %w", err)
	}
	if len(files) == 0 {
		return &ToolResult{Text: fmt.Sprintf("%s is in the library but holds NO track files right now. This read listed the album's own file records; an empty answer here means the files are genuinely absent, not unseen.", album.Title)}, nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — %d track file(s):\n", album.Title, len(files))
	for _, f := range files {
		fmt.Fprintf(&sb, "- %s (%.1f MB", path.Base(f.Path), float64(f.Size)/1e6)
		if f.Quality.Quality.Name != "" {
			fmt.Fprintf(&sb, ", %s", f.Quality.Quality.Name)
		}
		if f.DateAdded != nil {
			fmt.Fprintf(&sb, ", imported %s", f.DateAdded.UTC().Format("2006-01-02"))
		}
		sb.WriteString(")\n  ")
		sb.WriteString(trackMediaInfoLine(f))
		sb.WriteString("\n")
	}
	return &ToolResult{Text: sb.String()}, nil
}

func trackMediaInfoLine(f lidarr.TrackFile) string {
	if f.MediaInfo == nil {
		return "Lidarr has not analyzed this file's audio properties yet — codec/bitrate/sample-rate truth is UNKNOWN here (blindness, not absence)."
	}
	m := f.MediaInfo
	parts := make([]string, 0, 5)
	if m.AudioCodec != "" {
		parts = append(parts, m.AudioCodec)
	}
	if m.AudioBits != "" {
		parts = append(parts, m.AudioBits)
	}
	if m.AudioSampleRate != "" {
		parts = append(parts, m.AudioSampleRate)
	}
	if m.AudioChannels > 0 {
		parts = append(parts, fmt.Sprintf("%.1fch", m.AudioChannels))
	}
	if m.AudioBitRate != "" {
		parts = append(parts, m.AudioBitRate)
	}
	if len(parts) == 0 {
		return "audio properties unknown"
	}
	return "audio " + strings.Join(parts, " · ")
}
