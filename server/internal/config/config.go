package config

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	JWTSecret  string
	DBPath     string
	Port       int
	ServerName string
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
		EncryptionKeyFile: "/config/encryption.key",
		WebAuthnExtraOrigins: splitEnvList(
			os.Getenv("CANTINARR_WEBAUTHN_EXTRA_ORIGINS"),
		),
		AppleAppIDs:        splitEnvList(os.Getenv("CANTINARR_APPLE_APP_IDS")),
		AndroidPackageName: os.Getenv("CANTINARR_ANDROID_PACKAGE_NAME"),
		PushGatewayURL:     strings.TrimRight(os.Getenv("CANTINARR_PUSH_GATEWAY_URL"), "/"),
		PushAPIKey:         os.Getenv("CANTINARR_PUSH_API_KEY"),
		PushEnrollToken:    os.Getenv("CANTINARR_PUSH_ENROLL_TOKEN"),
	}

	if cfg.ServerName == "" {
		cfg.ServerName = "Cantinarr"
	}
	if cfg.AndroidPackageName == "" {
		cfg.AndroidPackageName = "codes.julian.cantinarr"
	}

	androidFingerprints := os.Getenv("CANTINARR_ANDROID_CERT_SHA256_FINGERPRINTS")
	if androidFingerprints == "" {
		androidFingerprints = os.Getenv("CANTINARR_ANDROID_CERT_SHA256")
	}
	var err error
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
	if portStr == "" {
		cfg.Port = 8585
	} else {
		p, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("invalid CANTINARR_PORT: %w", err)
		}
		cfg.Port = p
	}

	return cfg, nil
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
