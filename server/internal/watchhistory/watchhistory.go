// Package watchhistory is the provider-neutral contract for "what is playing
// and what was watched" on the household's media servers. Tautulli (Plex) and
// Tracearr (Plex, Jellyfin, Emby) each implement Provider, and the handler
// here serves both through one route family. Like mediaserver it is a leaf:
// it dials nothing and imports no other internal package, so instance can
// keep providers in its registry without an import cycle.
package watchhistory

import (
	"context"
	"strings"
	"time"
)

// serviceTypes are the instance service types that are watch-history
// providers: admin surfaces with a global default, never granted per user.
var serviceTypes = []string{"tautulli", "tracearr"}

// ServiceTypes returns the watch-history service types in a stable order.
func ServiceTypes() []string {
	return append([]string(nil), serviceTypes...)
}

// IsServiceType reports whether serviceType is a watch-history provider.
func IsServiceType(serviceType string) bool {
	for _, t := range serviceTypes {
		if t == serviceType {
			return true
		}
	}
	return false
}

// ServiceTypeList renders the provider types for error messages:
// "tautulli, tracearr".
func ServiceTypeList() string {
	return strings.Join(serviceTypes, ", ")
}

// MonitoredServer is one media server a provider watches. Tautulli watches
// exactly one Plex server and reports none here; Tracearr lists every server
// it monitors.
type MonitoredServer struct {
	ID            string
	Name          string
	Type          string // plex | jellyfin | emby
	Online        bool
	ActiveStreams int
}

// ServerInfo is what a provider says about itself; the connection test reads
// it and discards it.
type ServerInfo struct {
	Name    string
	Version string
	Servers []MonitoredServer
}

// Stream is one active playback session in the vocabulary the app renders.
type Stream struct {
	User            string
	Title           string
	FullTitle       string
	Player          string
	Product         string
	State           string // playing | paused | buffering | stopped
	ProgressPercent int
	Quality         string
	// StreamType is Tautulli's decision vocabulary, which the app matches by
	// substring: "direct play", "copy" (direct stream) or "transcode".
	StreamType    string
	BandwidthKbps int
	// MediaType, Server and ServerType are empty when a provider does not
	// know them; Tautulli names no server because it only ever has one.
	MediaType  string // movie | episode | track | live | photo | trailer | unknown
	Server     string
	ServerType string // plex | jellyfin | emby
}

// Activity is the current playback picture across everything the provider
// monitors.
type Activity struct {
	StreamCount        int
	TotalBandwidthKbps int
	Streams            []Stream
}

// HistoryEntry is one recorded play.
type HistoryEntry struct {
	User            string
	FullTitle       string
	Date            time.Time // zero when the provider did not say
	DurationSeconds int
	PercentComplete int
	Player          string
	Platform        string
	MediaType       string
	Server          string
	ServerType      string
}

// Coverage says what an answer was computed from, so an empty answer reads as
// absence ("nothing recorded since X") rather than blindness ("this code
// could not see"). Note is always set; the rest is filled when known.
type Coverage struct {
	Plays     int
	Since     time.Time
	Until     time.Time
	Truncated bool
	Note      string
}

// History is the most recent plays, newest first.
type History struct {
	Items    []HistoryEntry
	Coverage Coverage
}

// TitleCount is a play count for a movie or show.
type TitleCount struct {
	Title string
	Plays int
}

// UserCount is a play count for a viewer.
type UserCount struct {
	User  string
	Plays int
}

// Stats ranks what and who played over a window.
type Stats struct {
	TopMovies []TitleCount
	TopShows  []TitleCount
	TopUsers  []UserCount
	Coverage  Coverage
}

// Provider is one watch-history source. Implementations keep hosts and
// credentials out of every error they return: the handler serves those
// errors to admins verbatim.
type Provider interface {
	// ServerInfo proves the instance is reachable and the credential works.
	ServerInfo(ctx context.Context) (ServerInfo, error)
	// Activity is what is playing right now.
	Activity(ctx context.Context) (Activity, error)
	// History is the most recent plays, up to limit (always positive).
	History(ctx context.Context, limit int) (History, error)
	// Stats ranks plays over the last days (always positive).
	Stats(ctx context.Context, days int) (Stats, error)
}
