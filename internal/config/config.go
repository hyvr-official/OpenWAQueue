package config

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	OpenWABaseURL string
	OpenWAAPIKey  string
	AdminPassword string
	APIPort       string
	AdminPort     string
	DBPath        string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		OpenWABaseURL: strings.TrimRight(os.Getenv("OPENWA_BASE_URL"), "/"),
		OpenWAAPIKey:  os.Getenv("OPENWA_API_KEY"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		APIPort:       getOr("API_PORT", "3000"),
		AdminPort:     getOr("ADMIN_PORT", "5000"),
		DBPath:        getOr("DB_PATH", "data/queue.db"),
	}

	return cfg, nil
}

func getOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
