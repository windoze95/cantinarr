package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	TMDBAccessToken   string
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
	AllowedOrigins    []string
}

func (c *Config) TMDBEnabled() bool {
	return c.TMDBAccessToken != ""
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
	// Load .env file if present (dev convenience; does not override existing env vars).
	if err := godotenv.Load(); err == nil {
		log.Println("Loaded .env file")
	}

	cfg := &Config{
		TMDBAccessToken:   os.Getenv("CANTINARR_TMDB_ACCESS_TOKEN"),
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

	if cfg.TMDBAccessToken == "" {
		log.Println("WARNING: CANTINARR_TMDB_ACCESS_TOKEN not set – TMDB features will be disabled")
	}

	if cfg.DBPath == "" {
		cfg.DBPath = "/config/cantinarr.db"
	}

	if cfg.ServerName == "" {
		cfg.ServerName = "Cantinarr"
	}

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

	// Parse allowed CORS origins (comma-separated). Empty means same-origin only.
	if origins := os.Getenv("CANTINARR_ALLOWED_ORIGINS"); origins != "" {
		for _, o := range strings.Split(origins, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				cfg.AllowedOrigins = append(cfg.AllowedOrigins, o)
			}
		}
	}

	return cfg, nil
}
