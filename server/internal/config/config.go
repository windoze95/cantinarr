package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	TMDBKey           string
	AnthropicKey      string
	RadarrURL         string
	RadarrKey         string
	SonarrURL         string
	SonarrKey         string
	TraktClientID     string
	TraktClientSecret string
	JWTSecret         string
	DBPath            string
	Port              int
	ServerName        string
	AdminPassword     string
}

func (c *Config) RadarrEnabled() bool {
	return c.RadarrURL != "" && c.RadarrKey != ""
}

func (c *Config) SonarrEnabled() bool {
	return c.SonarrURL != "" && c.SonarrKey != ""
}

func (c *Config) TraktEnabled() bool {
	return c.TraktClientID != ""
}

func (c *Config) AIEnabled() bool {
	return c.AnthropicKey != ""
}

func Load() (*Config, error) {
	cfg := &Config{
		TMDBKey:           os.Getenv("CANTINARR_TMDB_KEY"),
		AnthropicKey:      os.Getenv("CANTINARR_ANTHROPIC_KEY"),
		RadarrURL:         os.Getenv("CANTINARR_RADARR_URL"),
		RadarrKey:         os.Getenv("CANTINARR_RADARR_KEY"),
		SonarrURL:         os.Getenv("CANTINARR_SONARR_URL"),
		SonarrKey:         os.Getenv("CANTINARR_SONARR_KEY"),
		TraktClientID:     os.Getenv("CANTINARR_TRAKT_CLIENT_ID"),
		TraktClientSecret: os.Getenv("CANTINARR_TRAKT_CLIENT_SECRET"),
		JWTSecret:         os.Getenv("CANTINARR_JWT_SECRET"),
		DBPath:            os.Getenv("CANTINARR_DB_PATH"),
		ServerName:        os.Getenv("CANTINARR_SERVER_NAME"),
		AdminPassword:     os.Getenv("CANTINARR_ADMIN_PASSWORD"),
	}

	if cfg.TMDBKey == "" {
		return nil, fmt.Errorf("CANTINARR_TMDB_KEY is required")
	}

	if cfg.DBPath == "" {
		cfg.DBPath = "/config/cantinarr.db"
	}

	if cfg.ServerName == "" {
		cfg.ServerName = "Cantinarr"
	}

	portStr := os.Getenv("CANTINARR_PORT")
	if portStr == "" {
		cfg.Port = 8484
	} else {
		p, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("invalid CANTINARR_PORT: %w", err)
		}
		cfg.Port = p
	}

	if cfg.JWTSecret == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("failed to generate JWT secret: %w", err)
		}
		cfg.JWTSecret = hex.EncodeToString(b)
	}

	return cfg, nil
}
