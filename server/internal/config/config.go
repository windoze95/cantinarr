package config

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	TMDBAccessToken   string
	AnthropicKey      string
	TraktClientID     string
	TraktClientSecret string
	JWTSecret         string
	DBPath            string
	Port              int
	ServerName        string
}

func (c *Config) TMDBEnabled() bool {
	return c.TMDBAccessToken != ""
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
		TraktClientID:     os.Getenv("CANTINARR_TRAKT_CLIENT_ID"),
		TraktClientSecret: os.Getenv("CANTINARR_TRAKT_CLIENT_SECRET"),
		JWTSecret:         os.Getenv("CANTINARR_JWT_SECRET"),
		DBPath:            os.Getenv("CANTINARR_DB_PATH"),
		ServerName:        os.Getenv("CANTINARR_SERVER_NAME"),
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

	return cfg, nil
}
