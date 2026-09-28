package chaptarr

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

// EnsureAuthorFormatConfig fills missing settings for one format. Re-read the
// full resource immediately before writing so settings unknown to this client
// and any choices made since request resolution survive the update.
func (c *Client) EnsureAuthorFormatConfig(id int, format string, qualityID, metadataID int, root string, disabledMetadataIDs map[int]bool) (*Author, error) {
	if id <= 0 || (format != "ebook" && format != "audiobook") {
		return nil, fmt.Errorf("invalid Chaptarr author format configuration")
	}
	path := fmt.Sprintf("/api/v1/author/%d", id)
	var body map[string]json.RawMessage
	if err := c.do("GET", path, nil, &body); err != nil {
		return nil, fmt.Errorf("read author settings: %w", err)
	}
	var current Author
	encoded, _ := json.Marshal(body)
	if err := json.Unmarshal(encoded, &current); err != nil || current.ID != id {
		return nil, &transporterr.Upstream{Message: "Chaptarr returned unreadable author settings", Transient: true}
	}
	getInt := func(key string) int {
		var value int
		_ = json.Unmarshal(body[key], &value)
		return value
	}
	var currentRoot string
	_ = json.Unmarshal(body[format+"RootFolderPath"], &currentRoot)
	changed := false
	set := func(key string, value any) {
		body[key], _ = json.Marshal(value)
		changed = true
	}
	if getInt(format+"QualityProfileId") <= 0 {
		set(format+"QualityProfileId", qualityID)
	}
	currentMetadata := getInt(format + "MetadataProfileId")
	if currentMetadata <= 0 || disabledMetadataIDs[currentMetadata] {
		set(format+"MetadataProfileId", metadataID)
	}
	if strings.TrimSpace(currentRoot) == "" {
		set(format+"RootFolderPath", root)
	}
	if changed {
		// An author edit is not an instruction to monitor their whole catalog.
		delete(body, "addOptions")
		if err := c.do("PUT", path, body, nil); err != nil {
			return nil, fmt.Errorf("save author format settings: %w", err)
		}
	}
	// Chaptarr can accept a write without applying every setting. Verify the
	// saved values before attempting the book add, and retry unreadable state.
	var saved Author
	if err := c.do("GET", path, nil, &saved); err != nil {
		return nil, fmt.Errorf("verify author format settings: %w", err)
	}
	q, m, folder := saved.EbookQualityProfileID, saved.EbookMetadataProfileID, saved.EbookRootFolderPath
	if format == "audiobook" {
		q, m, folder = saved.AudiobookQualityProfileID, saved.AudiobookMetadataProfileID, saved.AudiobookRootFolderPath
	}
	if saved.ID != id || q <= 0 || m <= 0 || disabledMetadataIDs[m] || strings.TrimSpace(folder) == "" {
		return nil, &transporterr.Upstream{Message: "Chaptarr did not retain the requested author format settings", Transient: true}
	}
	return &saved, nil
}
