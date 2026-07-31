package discover

import (
	"encoding/json"
	"strings"
)

// isEnglishOriginal reports whether a TMDB list entry is an English-language
// original. An entry TMDB did not classify is kept: this filter hides titles
// known to be foreign, never titles we merely failed to read.
func isEnglishOriginal(item json.RawMessage) bool {
	var probe struct {
		OriginalLanguage string `json:"original_language"`
	}
	if err := json.Unmarshal(item, &probe); err != nil {
		return true
	}
	return isEnglishLanguage(probe.OriginalLanguage)
}

// isEnglishLanguage reads the ISO-639-1 codes TMDB and Trakt both use. An empty
// code means "unclassified", which is kept for the same reason.
func isEnglishLanguage(code string) bool {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return true
	}
	// Tolerate a region suffix ("en-US") even though neither API sends one today.
	if i := strings.IndexAny(trimmed, "-_"); i > 0 {
		trimmed = trimmed[:i]
	}
	return strings.EqualFold(trimmed, "en")
}

// filterEnglishFeed drops non-English entries from a TMDB list payload,
// preserving the rest of the envelope and every surviving entry exactly as
// TMDB sent them. A payload it cannot parse passes through untouched, so a
// shape change upstream degrades to "unfiltered", never to "empty".
//
// total_pages and total_results are left alone: they still describe the
// upstream feed, so paging keeps walking every upstream page — each one just
// arrives thinner.
func filterEnglishFeed(data []byte) []byte {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return data
	}
	raw, ok := envelope["results"]
	if !ok {
		return data
	}
	var results []json.RawMessage
	if err := json.Unmarshal(raw, &results); err != nil {
		return data
	}

	kept := make([]json.RawMessage, 0, len(results))
	for _, item := range results {
		if isEnglishOriginal(item) {
			kept = append(kept, item)
		}
	}
	if len(kept) == len(results) {
		return data
	}

	encoded, err := json.Marshal(kept)
	if err != nil {
		return data
	}
	envelope["results"] = encoded
	out, err := json.Marshal(envelope)
	if err != nil {
		return data
	}
	return out
}
