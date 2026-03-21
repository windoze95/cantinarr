package config

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	JWTSecret  string
	DBPath     string
	Port       int
	ServerName string
}

func Load() (*Config, error) {
	// Load .env file if present (dev convenience; does not override existing env vars).
	if err := godotenv.Load(); err == nil {
		log.Println("Loaded .env file")
	}

	cfg := &Config{
		JWTSecret:  os.Getenv("CANTINARR_JWT_SECRET"),
		DBPath:     "/config/cantinarr.db",
		ServerName: os.Getenv("CANTINARR_SERVER_NAME"),
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
