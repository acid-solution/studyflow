package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"studyflow/internal/cache"
	"studyflow/internal/config"
	"studyflow/internal/database"
	"studyflow/internal/identity"
	"studyflow/internal/middleware"
	app "studyflow/internal/studyflow"

	"github.com/gin-gonic/gin"
)

// newCache builds the read-model cache, or returns nil when caching is switched
// off or the backend is unreachable. A nil cache makes the service read straight
// from MySQL, so a Redis outage degrades rather than breaks.
func newCache(cfg config.Config, logger *slog.Logger) app.Cache {
	if !cfg.CacheEnabled {
		logger.Info("cache disabled")
		return nil
	}
	store := cache.New(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB, cfg.CacheTTL, logger)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := store.Ping(ctx); err != nil {
		logger.Warn("cache unreachable at startup, serving from mysql", "addr", cfg.RedisAddr, "error", err)
		return nil
	}
	logger.Info("cache enabled", "addr", cfg.RedisAddr, "ttl", cfg.CacheTTL.String())
	return store
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	db, err := database.OpenMySQL(cfg.MySQLDSN, cfg.MigrationsDir)
	if err != nil {
		log.Fatal(err)
	}
	authenticator := identity.NewAuthenticator(cfg.AuthJWKSURL, cfg.AuthIssuer, cfg.AuthAudience, cfg.JWKSCacheTTL)
	service := app.NewServiceWithCache(db, newCache(cfg, logger))
	handler := app.NewHTTPHandler(service)

	router := gin.New()
	router.Use(middleware.RequestID(), middleware.RequestLogger(logger), gin.Recovery(), cors(cfg.FrontendURL))
	app.RegisterRoutes(router, handler, authenticator.Middleware(), db)
	app.RegisterMCP(router, service, authenticator, logger)
	frontendIndex := filepath.Join("frontend", "dist", "index.html")
	if _, err := os.Stat(frontendIndex); err == nil {
		router.Static("/assets", filepath.Join("frontend", "dist", "assets"))
		router.GET("/", func(c *gin.Context) { c.File(frontendIndex) })
		router.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") {
				c.JSON(http.StatusNotFound, gin.H{"code": 10002, "message": "接口不存在", "data": nil})
				return
			}
			c.File(frontendIndex)
		})
	}
	if err := router.Run(":" + cfg.Port); err != nil {
		log.Fatal(err)
	}
}

func cors(allowedOrigin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && strings.EqualFold(origin, allowedOrigin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID, Idempotency-Key")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
