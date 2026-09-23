package config

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port          string
	MySQLDSN      string
	MigrationsDir string
	AuthJWKSURL   string
	AuthIssuer    string
	AuthAudience  string
	FrontendURL   string
	JWKSCacheTTL  time.Duration
}

func Load() (Config, error) {
	_ = godotenv.Load()
	cfg := Config{
		Port:          valueOrDefault("PORT", "8081"),
		MySQLDSN:      strings.TrimSpace(os.Getenv("MYSQL_DSN")),
		MigrationsDir: valueOrDefault("MIGRATIONS_DIR", "migrations"),
		AuthJWKSURL:   valueOrDefault("AUTH_JWKS_URL", "http://127.0.0.1:18082/.well-known/jwks.json"),
		AuthIssuer:    valueOrDefault("AUTH_ISSUER", "shared-auth"),
		AuthAudience:  valueOrDefault("AUTH_AUDIENCE", "studyflow"),
		FrontendURL:   valueOrDefault("FRONTEND_URL", "http://127.0.0.1:5174"),
		JWKSCacheTTL:  10 * time.Minute,
	}
	if cfg.MySQLDSN == "" {
		return Config{}, errors.New("MYSQL_DSN is required")
	}
	if value := strings.TrimSpace(os.Getenv("JWKS_CACHE_TTL")); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return Config{}, errors.New("JWKS_CACHE_TTL must be a positive duration")
		}
		cfg.JWKSCacheTTL = duration
	}
	return cfg, nil
}

func valueOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
