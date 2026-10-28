// Package contentnotify defines channel-independent identities used when
// matching an import to a saved request. It owns no transport or preferences.
package contentnotify

import "context"

// Import is positive import evidence. TV requires the exact native episode IDs;
// a title-only event cannot qualify a requester-only recipient.
type Import struct {
	MediaType  string
	InstanceID string
	TmdbID     int
	ForeignID  string
	Format     string
	EpisodeIDs []int
	// RecipientEpisodeIDs narrows evidence after a recipient muted an earlier
	// witness. Absent entries use EpisodeIDs; present empty entries permit none.
	RecipientEpisodeIDs map[int64][]int
}

type RequestMatcher interface {
	RequestedContentRecipients(context.Context, Import, []int64) ([]int64, error)
}

// Unit names content, not its current file, so upgrades retain its identity.
type Unit struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Format    string `json:"format,omitempty"`
	ForeignID string `json:"foreign_id,omitempty"`
}

type Subject struct {
	RequestID  int64  `json:"request_id,omitempty"`
	IssueID    int64  `json:"issue_id,omitempty"`
	Title      string `json:"title"`
	MediaType  string `json:"media_type"`
	TmdbID     int    `json:"tmdb_id,omitempty"`
	ForeignID  string `json:"foreign_id,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	Library    string `json:"library,omitempty"`
	PosterURL  string `json:"poster_url,omitempty"`
}

type Availability struct {
	Subject  Subject
	Units    []Unit
	Baseline bool
}
