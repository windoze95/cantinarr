package downloads

import "time"

// Activity is a per-user projection. Count is null whenever the exact number
// cannot be established; a partial snapshot must never masquerade as zero.
type Activity struct {
	Scope     string           `json:"scope"`
	UserScope string           `json:"user_scope"`
	Count     *int             `json:"count"`
	Complete  bool             `json:"complete"`
	Stale     bool             `json:"stale"`
	FetchedAt time.Time        `json:"fetched_at"`
	Sources   []ActivitySource `json:"sources"`
	Groups    []ContentGroup   `json:"groups,omitempty"`
	Jobs      []ActivityJob    `json:"jobs,omitempty"`
}

type ActivitySource struct {
	InstanceID string `json:"instance_id"`
	Name       string `json:"name"`
	Available  bool   `json:"available"`
	Message    string `json:"message,omitempty"`
}

// Jobs are normalized once and referenced from groups/children, so a pack
// spanning multiple seasons never contributes its bytes or count twice.
type ActivityJob struct {
	ID            string      `json:"id"`
	Status        string      `json:"status"`
	SizeBytes     int64       `json:"size_bytes"`
	SizeLeftBytes int64       `json:"size_left_bytes"`
	Progress      float64     `json:"progress"`
	SpeedBPS      int64       `json:"speed_bps"`
	Control       *JobControl `json:"control,omitempty"`
	// Name is the client's or arr's own name for the job. Requesters never
	// receive it.
	Name string `json:"name,omitempty"`
}

type JobControl struct {
	InstanceID  string `json:"instance_id"`
	ItemID      string `json:"item_id"`
	ServiceType string `json:"service_type"`
	ClientName  string `json:"client_name"`
}

type ContentGroup struct {
	ID           string         `json:"id"`
	InstanceID   string         `json:"instance_id,omitempty"`
	InstanceName string         `json:"instance_name,omitempty"`
	MediaType    string         `json:"media_type"`
	Title        string         `json:"title"`
	Year         int            `json:"year,omitempty"`
	Creator      string         `json:"creator,omitempty"`
	Format       string         `json:"format,omitempty"`
	Artwork      string         `json:"artwork,omitempty"`
	JobIDs       []string       `json:"job_ids"`
	Children     []ContentChild `json:"children"`
	DetailsKnown bool           `json:"details_known"`
	Progress     float64        `json:"progress"`
}

type ContentChild struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Season  *int     `json:"season,omitempty"`
	Episode int      `json:"episode,omitempty"`
	Disc    int      `json:"disc,omitempty"`
	Track   string   `json:"track,omitempty"`
	JobIDs  []string `json:"job_ids"`
}
