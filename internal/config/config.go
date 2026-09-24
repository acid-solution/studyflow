package config

import (
	"errors"
	"os"
	"strconv"
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

	// Caching is off unless CACHE_ENABLED is set. The service falls back to MySQL
	// whenever Redis is unreachable, so this only decides whether it is tried.
	CacheEnabled  bool
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	CacheTTL      time.Duration
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

		CacheEnabled:  strings.EqualFold(valueOrDefault("CACHE_ENABLED", "false"), "true"),
		RedisAddr:     valueOrDefault("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword: strings.TrimSpace(os.Getenv("REDIS_PASSWORD")),
		CacheTTL:      30 * time.Second,
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
	if value := strings.TrimSpace(os.Getenv("CACHE_TTL")); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return Config{}, errors.New("CACHE_TTL must be a positive duration")
		}
		cfg.CacheTTL = duration
	}
	if value := strings.TrimSpace(os.Getenv("REDIS_DB")); value != "" {
		number, err := strconv.Atoi(value)
		if err != nil || number < 0 {
			return Config{}, errors.New("REDIS_DB must be a non-negative integer")
		}
		cfg.RedisDB = number
	}
	return cfg, nil
}

func valueOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
