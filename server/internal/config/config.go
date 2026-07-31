package config

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	JWTSecret  string
	DBPath     string
	Port       int
	ServerName string
	// PublicURL is the trusted origin used to build the callback that
	// Radarr/Sonarr POST webhooks to, so it must be resolvable and reachable
	// from the arr containers themselves (cluster-internal origins are valid
	// and often correct). Empty falls back to the direct request origin;
	// forwarded headers are never trusted for callback credentials.
	PublicURL string
	// OAuthIssuer is the canonical external origin for inbound MCP OAuth.
	// When set, OAuth metadata, authorization responses, resource audiences,
	// and MCP browser origin checks use it instead of request headers.
	OAuthIssuer string
	// MCPAllowedOrigins are additional browser origins allowed to call /mcp.
	// Requests without Origin (native/server MCP clients) are unaffected.
	MCPAllowedOrigins []string
	// EncryptionKeyFile backs secrets-at-rest when CANTINARR_ENCRYPTION_KEY
	// is not set; it lives next to the database.
	EncryptionKeyFile string
	// WebAuthnExtraOrigins are trusted in addition to the request origin.
	// Native Android passkeys use android:apk-key-hash origins.
	WebAuthnExtraOrigins []string
	// AppleAppIDs are TeamID.BundleID entries served in the AASA file.
	AppleAppIDs []string
	// AndroidPackageName and AndroidCertFingerprints are served in assetlinks.json.
	AndroidPackageName      string
	AndroidCertFingerprints []string
	// PushGatewayURL enables the self-hosted push gateway when set (empty =
	// disabled). PushAPIKey is an explicit per-app key and is optional: if it is
	// empty while the gateway URL is set, the server auto-enrolls with the gateway
	// on first start and persists the issued key (see ensurePushAPIKey). An
	// explicit key always wins. PushEnrollToken is sent as X-Enroll-Token during
	// auto-enroll, needed only when the gateway uses gated enrollment.
	PushGatewayURL  string
	PushAPIKey      string
	PushEnrollToken string
	// DisableUpdateCheck turns off the periodic GitHub release check that powers
	// the admin "update available" banner (CANTINARR_DISABLE_UPDATE_CHECK).
	DisableUpdateCheck bool
	// CodexBin optionally overrides the Codex app-server executable. Empty lets
	// the adapter discover codex-app-server first and the full codex CLI second.
	CodexBin string
	// CodexRuntimeDir is the private memory-backed root where decrypted, per-user
	// Codex auth state may exist while an app-server process is running. Empty
	// lets the adapter use /dev/shm/cantinarr-codex when available.
	CodexRuntimeDir string
	// MediaDownloadRoots are the deployment-owned read-only filesystem
	// boundaries from which completed media may be served. Per-instance mappings
	// may target these roots or their descendants; empty disables media delivery.
	MediaDownloadRoots []string
}

func Load() (*Config, error) {
	// Load .env file if present (dev convenience; does not override existing env vars).
	if err := godotenv.Load(); err == nil {
		log.Println("Loaded .env file")
	}

	cfg := &Config{
		JWTSecret:         os.Getenv("CANTINARR_JWT_SECRET"),
		DBPath:            "/config/cantinarr.db",
		ServerName:        os.Getenv("CANTINARR_SERVER_NAME"),
		PublicURL:         strings.TrimRight(os.Getenv("CANTINARR_PUBLIC_URL"), "/"),
		OAuthIssuer:       strings.TrimRight(strings.TrimSpace(os.Getenv("CANTINARR_OAUTH_ISSUER")), "/"),
		MCPAllowedOrigins: splitEnvList(os.Getenv("CANTINARR_MCP_ALLOWED_ORIGINS")),
		EncryptionKeyFile: "/config/encryption.key",
		WebAuthnExtraOrigins: splitEnvList(
			os.Getenv("CANTINARR_WEBAUTHN_EXTRA_ORIGINS"),
		),
		AppleAppIDs:        splitEnvList(os.Getenv("CANTINARR_APPLE_APP_IDS")),
		AndroidPackageName: os.Getenv("CANTINARR_ANDROID_PACKAGE_NAME"),
		PushGatewayURL:     strings.TrimRight(os.Getenv("CANTINARR_PUSH_GATEWAY_URL"), "/"),
		PushAPIKey:         os.Getenv("CANTINARR_PUSH_API_KEY"),
		PushEnrollToken:    os.Getenv("CANTINARR_PUSH_ENROLL_TOKEN"),
		CodexBin:           strings.TrimSpace(os.Getenv("CANTINARR_CODEX_BIN")),
		CodexRuntimeDir:    strings.TrimSpace(os.Getenv("CANTINARR_CODEX_RUNTIME_DIR")),
		MediaDownloadRoots: splitEnvList(os.Getenv("CANTINARR_MEDIA_ROOTS")),
	}

	cfg.DisableUpdateCheck = envBool(os.Getenv("CANTINARR_DISABLE_UPDATE_CHECK"))

	if cfg.ServerName == "" {
		cfg.ServerName = "Cantinarr"
	}
	if cfg.AndroidPackageName == "" {
		cfg.AndroidPackageName = "codes.julian.cantinarr"
	}
	if err := validatePublicURL(cfg.PublicURL); err != nil {
		return nil, fmt.Errorf("invalid CANTINARR_PUBLIC_URL: %w", err)
	}
	if cfg.OAuthIssuer != "" {
		normalized, err := normalizeHTTPOrigin(cfg.OAuthIssuer)
		if err != nil {
			return nil, fmt.Errorf("invalid CANTINARR_OAUTH_ISSUER: %w", err)
		}
		issuerURL, err := url.Parse(normalized)
		if err != nil || issuerURL.Scheme != "https" {
			return nil, fmt.Errorf("invalid CANTINARR_OAUTH_ISSUER: must use https")
		}
		cfg.OAuthIssuer = normalized
	}
	for i, origin := range cfg.MCPAllowedOrigins {
		normalized, err := normalizeHTTPOrigin(origin)
		if err != nil {
			return nil, fmt.Errorf("invalid CANTINARR_MCP_ALLOWED_ORIGINS entry %q: %w", origin, err)
		}
		cfg.MCPAllowedOrigins[i] = normalized
	}
	if cfg.CodexRuntimeDir != "" && !filepath.IsAbs(cfg.CodexRuntimeDir) {
		return nil, fmt.Errorf("invalid CANTINARR_CODEX_RUNTIME_DIR: must be an absolute path")
	}
	var err error
	cfg.MediaDownloadRoots, err = normalizeMediaDownloadRoots(cfg.MediaDownloadRoots)
	if err != nil {
		return nil, fmt.Errorf("invalid CANTINARR_MEDIA_ROOTS: %w", err)
	}

	androidFingerprints := os.Getenv("CANTINARR_ANDROID_CERT_SHA256_FINGERPRINTS")
	if androidFingerprints == "" {
		androidFingerprints = os.Getenv("CANTINARR_ANDROID_CERT_SHA256")
	}
	cfg.AndroidCertFingerprints, err = normalizeAndroidFingerprints(
		splitEnvList(androidFingerprints),
	)
	if err != nil {
		return nil, err
	}
	cfg.WebAuthnExtraOrigins = append(
		cfg.WebAuthnExtraOrigins,
		androidOrigins(cfg.AndroidCertFingerprints)...,
	)

	portStr := os.Getenv("CANTINARR_PORT")
	switch {
	case portStr == "":
		cfg.Port = 8585
	case isKubernetesServiceLinkPort(portStr):
		// Kubernetes injects CANTINARR_PORT=tcp://<service-ip>:<port> when a
		// Service named cantinarr has service links enabled. It is not an app
		// setting, so retain the default listen port.
		log.Printf("CANTINARR_PORT=%q is a Kubernetes service link, not a port setting; listening on the default port 8585", portStr)
		cfg.Port = 8585
	default:
		p, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("invalid CANTINARR_PORT: %w", err)
		}
		cfg.Port = p
	}

	return cfg, nil
}

func normalizeMediaDownloadRoots(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !filepath.IsAbs(value) {
			return nil, fmt.Errorf("entry %q must be an absolute path", value)
		}
		cleaned := filepath.Clean(value)
		if filepath.Dir(cleaned) == cleaned {
			return nil, fmt.Errorf("filesystem root is too broad")
		}
		resolved, err := filepath.EvalSymlinks(cleaned)
		if err != nil {
			return nil, fmt.Errorf("entry %q is not accessible: %w", value, err)
		}
		if filepath.Dir(resolved) == resolved {
			return nil, fmt.Errorf("entry %q resolves to the filesystem root", value)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, fmt.Errorf("entry %q is not accessible: %w", value, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("entry %q is not a directory", value)
		}
		// Retain the cleaned lexical path because this is the namespace visible to
		// Cantinarr and therefore the destination namespace admins select in their
		// per-instance mappings. The resolved path above validates the target and
		// rejects aliases of the filesystem root.
		if seen[cleaned] {
			continue
		}
		seen[cleaned] = true
		result = append(result, cleaned)
	}
	return result, nil
}

func isKubernetesServiceLinkPort(value string) bool {
	serviceHost := os.Getenv("CANTINARR_SERVICE_HOST")
	servicePort := os.Getenv("CANTINARR_SERVICE_PORT")
	if serviceHost == "" || servicePort == "" {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "tcp" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if net.ParseIP(u.Hostname()) == nil || u.Hostname() != serviceHost || u.Port() != servicePort {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	return err == nil && port > 0 && port <= 65535
}

func validatePublicURL(value string) error {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil {
		return err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("must be an absolute http or https origin")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("must contain only a scheme and host")
	}
	return nil
}

func normalizeHTTPOrigin(value string) (string, error) {
	value = strings.TrimSpace(value)
	if err := validatePublicURL(value); err != nil {
		return "", err
	}
	u, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = ""
	return strings.TrimRight(u.String(), "/"), nil
}

func splitEnvList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

// envBool parses a boolean environment variable. It accepts 1/true/yes/on
// (case-insensitive) as true; everything else (including empty) is false.
func envBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func normalizeAndroidFingerprints(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.ToUpper(strings.ReplaceAll(value, ":", ""))
		if len(normalized) != 64 {
			return nil, fmt.Errorf("invalid Android SHA-256 fingerprint %q", value)
		}
		if _, err := hex.DecodeString(normalized); err != nil {
			return nil, fmt.Errorf("invalid Android SHA-256 fingerprint %q: %w", value, err)
		}
		var builder strings.Builder
		for i := 0; i < len(normalized); i += 2 {
			if i > 0 {
				builder.WriteString(":")
			}
			builder.WriteString(normalized[i : i+2])
		}
		result = append(result, builder.String())
	}
	return result, nil
}

func androidOrigins(fingerprints []string) []string {
	origins := make([]string, 0, len(fingerprints))
	for _, fingerprint := range fingerprints {
		raw := strings.ReplaceAll(fingerprint, ":", "")
		bytes, err := hex.DecodeString(raw)
		if err != nil {
			continue
		}
		origins = append(origins,
			"android:apk-key-hash:"+base64.RawURLEncoding.EncodeToString(bytes),
		)
	}
	return origins
}
