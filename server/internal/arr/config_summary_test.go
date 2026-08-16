package arr

import (
	"encoding/json"
	"strings"
	"testing"
)

// The load-bearing property: indexer and download-client payloads carry API
// keys and passwords in their dynamic fields array, and NOTHING outside the
// known-safe allowlist may ever reach a summary.
func TestConfigSummaryNeverLeaksSecrets(t *testing.T) {
	raw := json.RawMessage(`{
		"name": "NZBgeek",
		"protocol": "usenet",
		"enableRss": true,
		"enableAutomaticSearch": true,
		"priority": 25,
		"fields": [
			{"name": "apiKey", "value": "SECRET-KEY-123"},
			{"name": "password", "value": "hunter2"},
			{"name": "baseUrl", "value": "https://user:pass@indexer.example"},
			{"name": "minimumSeeders", "value": 0}
		]
	}`)
	entries := SummarizeConfigSection(ConfigIndexers, []json.RawMessage{raw})
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	text := entries[0].Name + " " + entries[0].Detail
	for _, secret := range []string{"SECRET-KEY-123", "hunter2", "user:pass", "indexer.example"} {
		if strings.Contains(text, secret) {
			t.Fatalf("summary leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "min seeders 0") {
		t.Fatalf("the prevention-relevant value is missing: %s", text)
	}
}

func TestConfigSummarySections(t *testing.T) {
	delay := SummarizeConfigSection(ConfigDelayProfiles, []json.RawMessage{json.RawMessage(
		`{"usenetDelay": 0, "torrentDelay": 120, "enableUsenet": true, "enableTorrent": true, "preferredProtocol": "usenet"}`)})
	if len(delay) != 1 || !strings.Contains(delay[0].Detail, "torrent delay (min) 120") {
		t.Fatalf("delay summary = %+v", delay)
	}
	mapping := SummarizeConfigSection(ConfigRemotePathMappings, []json.RawMessage{json.RawMessage(
		`{"host": "qbittorrent", "remotePath": "/downloads/", "localPath": "/media-server/downloads/"}`)})
	if len(mapping) != 1 || !strings.Contains(mapping[0].Detail, "/downloads/ -> /media-server/downloads/") {
		t.Fatalf("mapping summary = %+v", mapping)
	}
}
