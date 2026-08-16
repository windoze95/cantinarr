package arr

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Config sections the read tool can summarize. These are the settings the
// prevention catalog names but nothing could see — the difference between
// "check your indexer's minimum seeders" and "NZBgeek min seeders = 0".
const (
	ConfigIndexers           = "indexers"
	ConfigDelayProfiles      = "delay_profiles"
	ConfigReleaseProfiles    = "release_profiles"
	ConfigDownloadClients    = "download_clients"
	ConfigRemotePathMappings = "remote_path_mappings"
)

// ConfigEntry is one bounded, secret-free line of a config section. This is
// the ONLY shape that ever leaves a client for these endpoints: indexer and
// download-client payloads carry API keys and passwords in their dynamic
// fields array, so the summarizer extracts a known-safe allowlist of values
// and the raw JSON never crosses the client boundary.
type ConfigEntry struct {
	Name   string
	Detail string
}

// ValidConfigSection reports whether a section name is known.
func ValidConfigSection(section string) bool {
	switch section {
	case ConfigIndexers, ConfigDelayProfiles, ConfigReleaseProfiles,
		ConfigDownloadClients, ConfigRemotePathMappings:
		return true
	}
	return false
}

// safeFieldValues is the exact allowlist of dynamic-field names whose values
// may appear in a summary. Everything else in a fields array — apiKey,
// password, username, url userinfo, cookies — is dropped unread.
var safeFieldValues = map[string]string{
	"minimumSeeders":   "min seeders",
	"seedRatio":        "seed ratio",
	"movieCategory":    "category",
	"tvCategory":       "category",
	"bookCategory":     "category",
	"musicCategory":    "category",
	"category":         "category",
	"recentTvPriority": "recent priority",
}

// SummarizeConfigSection renders one section's raw records into bounded,
// secret-free entries. Unknown record shapes degrade to a name-only entry —
// never to echoing the payload.
func SummarizeConfigSection(section string, raws []json.RawMessage) []ConfigEntry {
	out := make([]ConfigEntry, 0, len(raws))
	for _, raw := range raws {
		var record map[string]any
		if err := json.Unmarshal(raw, &record); err != nil {
			continue
		}
		out = append(out, summarizeConfigRecord(section, record))
	}
	return out
}

func summarizeConfigRecord(section string, record map[string]any) ConfigEntry {
	name, _ := record["name"].(string)
	var parts []string
	onOff := func(key, label string) {
		if v, ok := record[key].(bool); ok {
			state := "off"
			if v {
				state = "on"
			}
			parts = append(parts, label+" "+state)
		}
	}
	num := func(key, label string) {
		if v, ok := record[key].(float64); ok {
			parts = append(parts, fmt.Sprintf("%s %g", label, v))
		}
	}
	switch section {
	case ConfigIndexers:
		if v, ok := record["protocol"].(string); ok && v != "" {
			parts = append(parts, v)
		}
		onOff("enableRss", "rss")
		onOff("enableAutomaticSearch", "auto-search")
		num("priority", "priority")
		parts = append(parts, safeFieldSummaries(record)...)
	case ConfigDelayProfiles:
		if name == "" {
			name = "delay profile"
		}
		num("usenetDelay", "usenet delay (min)")
		num("torrentDelay", "torrent delay (min)")
		onOff("enableUsenet", "usenet")
		onOff("enableTorrent", "torrent")
		if v, ok := record["preferredProtocol"].(string); ok && v != "" {
			parts = append(parts, "prefers "+v)
		}
	case ConfigReleaseProfiles:
		if name == "" {
			name = "release profile"
		}
		onOff("enabled", "enabled")
		parts = append(parts, termCount(record, "required", "required term(s)"))
		parts = append(parts, termCount(record, "ignored", "ignored term(s)"))
	case ConfigDownloadClients:
		if v, ok := record["protocol"].(string); ok && v != "" {
			parts = append(parts, v)
		}
		onOff("enable", "enabled")
		num("priority", "priority")
		parts = append(parts, safeFieldSummaries(record)...)
	case ConfigRemotePathMappings:
		host, _ := record["host"].(string)
		remote, _ := record["remotePath"].(string)
		local, _ := record["localPath"].(string)
		if name == "" {
			name = host
		}
		parts = append(parts, fmt.Sprintf("%s -> %s", remote, local))
	}
	cleaned := parts[:0]
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			cleaned = append(cleaned, p)
		}
	}
	return ConfigEntry{Name: name, Detail: strings.Join(cleaned, " · ")}
}

func termCount(record map[string]any, key, label string) string {
	switch v := record[key].(type) {
	case []any:
		return fmt.Sprintf("%d %s", len(v), label)
	case string:
		if strings.TrimSpace(v) == "" {
			return "0 " + label
		}
		return fmt.Sprintf("%d %s", len(strings.Split(strings.TrimSpace(v), "\n")), label)
	}
	return ""
}

func safeFieldSummaries(record map[string]any) []string {
	fields, ok := record["fields"].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, f := range fields {
		field, ok := f.(map[string]any)
		if !ok {
			continue
		}
		fname, _ := field["name"].(string)
		label, safe := safeFieldValues[fname]
		if !safe {
			continue // apiKey, password, and every unknown name die here, unread.
		}
		switch v := field["value"].(type) {
		case float64:
			out = append(out, fmt.Sprintf("%s %g", label, v))
		case string:
			if v != "" {
				out = append(out, fmt.Sprintf("%s %s", label, v))
			}
		}
	}
	return out
}
