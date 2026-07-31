package chaptarr

import (
	"encoding/json"
	"testing"
)

func TestAuthorDecodesLegacyAndPerFormatConfiguration(t *testing.T) {
	var author Author
	err := json.Unmarshal([]byte(`{
		"id":2101,
		"authorName":"Mara Vale",
		"foreignAuthorId":"hc:author-2001",
		"path":"/legacy/books",
		"qualityProfileId":90,
		"metadataProfileId":91,
		"ebookQualityProfileId":11,
		"audiobookQualityProfileId":12,
		"ebookMetadataProfileId":21,
		"audiobookMetadataProfileId":22,
		"ebookRootFolderPath":"/library/ebooks",
		"audiobookRootFolderPath":"/library/audiobooks",
		"ebookMonitorFuture":true,
		"audiobookMonitorFuture":false
	}`), &author)
	if err != nil {
		t.Fatalf("unmarshal author: %v", err)
	}

	if author.Path != "/legacy/books" || author.QualityProfileID != 90 || author.MetadataProfileID != 91 {
		t.Fatalf("legacy author configuration = %+v", author)
	}
	if author.EbookQualityProfileID != 11 || author.AudiobookQualityProfileID != 12 {
		t.Fatalf("quality profile ids = ebook %d audiobook %d", author.EbookQualityProfileID, author.AudiobookQualityProfileID)
	}
	if author.EbookMetadataProfileID != 21 || author.AudiobookMetadataProfileID != 22 {
		t.Fatalf("metadata profile ids = ebook %d audiobook %d", author.EbookMetadataProfileID, author.AudiobookMetadataProfileID)
	}
	if author.EbookRootFolderPath != "/library/ebooks" || author.AudiobookRootFolderPath != "/library/audiobooks" {
		t.Fatalf("root paths = ebook %q audiobook %q", author.EbookRootFolderPath, author.AudiobookRootFolderPath)
	}
	if !author.EbookMonitorFuture || author.AudiobookMonitorFuture {
		t.Fatalf("monitor future = ebook %t audiobook %t", author.EbookMonitorFuture, author.AudiobookMonitorFuture)
	}
}

func TestProfilesDecodeFormatDiscriminators(t *testing.T) {
	var quality []QualityProfile
	if err := json.Unmarshal([]byte(`[
		{"id":11,"name":"Ebook Standard","profileType":"ebook"},
		{"id":12,"name":"Audiobook Standard","profileType":"audiobook"}
	]`), &quality); err != nil {
		t.Fatalf("unmarshal quality profiles: %v", err)
	}
	if len(quality) != 2 || quality[0].ProfileType != "ebook" || quality[1].ProfileType != "audiobook" {
		t.Fatalf("quality profiles = %+v", quality)
	}

	var metadata []MetadataProfile
	if err := json.Unmarshal([]byte(`[
		{"id":20,"name":"None","profileType":0},
		{"id":21,"name":"Ebook Default","profileType":2},
		{"id":22,"name":"Audiobook Default","profileType":"1"},
		{"id":23,"name":"Future Text","profileType":"audiobook"},
		{"id":24,"name":"Legacy Missing"}
	]`), &metadata); err != nil {
		t.Fatalf("unmarshal metadata profiles: %v", err)
	}
	want := []string{"0", "2", "1", "audiobook", ""}
	if len(metadata) != len(want) {
		t.Fatalf("metadata profile count = %d, want %d", len(metadata), len(want))
	}
	for i := range want {
		if metadata[i].ProfileType != want[i] {
			t.Errorf("metadata[%d].ProfileType = %q, want %q", i, metadata[i].ProfileType, want[i])
		}
	}
}

func TestRootFolderDecodesCompatibilityShapes(t *testing.T) {
	var roots []RootFolder
	err := json.Unmarshal([]byte(`[
		{
			"id":1,
			"name":"Audiobooks",
			"path":"/library/audiobooks",
			"audiobook":true,
			"ebook":{"writeMetadata":false},
			"isEffectiveDefaultAudiobook":true
		},
		{
			"id":2,
			"name":"Offline",
			"path":"/library/offline",
			"accessible":false,
			"ebook":true,
			"audiobook":{"writeMetadata":false},
			"isEffectiveDefaultEbook":true
		},
		{
			"id":3,
			"name":"Legacy",
			"path":"/library/legacy",
			"accessible":true,
			"ebook":false,
			"audiobook":false
		},
		{
			"id":4,
			"name":"Null accessibility",
			"path":"/library/null",
			"accessible":null
		}
	]`), &roots)
	if err != nil {
		t.Fatalf("unmarshal root folders: %v", err)
	}
	if len(roots) != 4 {
		t.Fatalf("root count = %d, want 4", len(roots))
	}
	if !roots[0].IsAccessible() || roots[0].Ebook || !roots[0].Audiobook || !roots[0].IsEffectiveDefaultAudiobook {
		t.Fatalf("audiobook root = %+v", roots[0])
	}
	if roots[1].IsAccessible() || !roots[1].Ebook || roots[1].Audiobook || !roots[1].IsEffectiveDefaultEbook {
		t.Fatalf("offline ebook root = %+v", roots[1])
	}
	if !roots[2].IsAccessible() || roots[2].Ebook || roots[2].Audiobook {
		t.Fatalf("legacy root = %+v", roots[2])
	}
	if !roots[3].IsAccessible() {
		t.Fatalf("null accessibility should retain compatibility default: %+v", roots[3])
	}
}

func TestAddBookRequestMarshalsLegacyAndPerFormatConfiguration(t *testing.T) {
	audioMonitored := true
	ebookMonitored := false
	req := AddBookRequest{
		ForeignBookID:              "hc:work-1001",
		AuthorID:                   2101,
		Title:                      "The Clockwork Orchard",
		MediaType:                  "audiobook",
		EbookMonitored:             &ebookMonitored,
		AudiobookMonitored:         &audioMonitored,
		RootFolderPath:             "/library/audiobooks",
		EbookQualityProfileID:      11,
		AudiobookQualityProfileID:  12,
		EbookMetadataProfileID:     21,
		AudiobookMetadataProfileID: 22,
		EbookRootFolderPath:        "/library/ebooks",
		AudiobookRootFolderPath:    "/library/audiobooks",
		Author: AddAuthorRequest{
			ID:                         2101,
			AuthorName:                 "Mara Vale",
			ForeignAuthorID:            "hc:author-2001",
			QualityProfileID:           90,
			MetadataProfileID:          91,
			RootFolderPath:             "/legacy/books",
			EbookQualityProfileID:      11,
			AudiobookQualityProfileID:  12,
			EbookMetadataProfileID:     21,
			AudiobookMetadataProfileID: 22,
			EbookRootFolderPath:        "/library/ebooks",
			AudiobookRootFolderPath:    "/library/audiobooks",
			EbookMonitorFuture:         false,
			AudiobookMonitorFuture:     true,
			Monitored:                  true,
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal add book request: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode marshaled request: %v", err)
	}

	if body["authorId"] != float64(2101) || body["rootFolderPath"] != "/library/audiobooks" {
		t.Fatalf("top-level identity/root = %#v", body)
	}
	for key, value := range map[string]any{
		"ebookQualityProfileId":      float64(11),
		"audiobookQualityProfileId":  float64(12),
		"ebookMetadataProfileId":     float64(21),
		"audiobookMetadataProfileId": float64(22),
		"ebookRootFolderPath":        "/library/ebooks",
		"audiobookRootFolderPath":    "/library/audiobooks",
	} {
		if body[key] != value {
			t.Errorf("top-level %s = %#v, want %#v", key, body[key], value)
		}
	}

	author, ok := body["author"].(map[string]any)
	if !ok {
		t.Fatalf("author = %#v, want object", body["author"])
	}
	for key, value := range map[string]any{
		"id":                         float64(2101),
		"qualityProfileId":           float64(90),
		"metadataProfileId":          float64(91),
		"rootFolderPath":             "/legacy/books",
		"ebookQualityProfileId":      float64(11),
		"audiobookQualityProfileId":  float64(12),
		"ebookMetadataProfileId":     float64(21),
		"audiobookMetadataProfileId": float64(22),
		"ebookRootFolderPath":        "/library/ebooks",
		"audiobookRootFolderPath":    "/library/audiobooks",
		"ebookMonitorFuture":         false,
		"audiobookMonitorFuture":     true,
	} {
		if author[key] != value {
			t.Errorf("author.%s = %#v, want %#v", key, author[key], value)
		}
	}
}
