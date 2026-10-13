package main

import (
	"context"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
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
// off. A store that cannot reach Redis at startup is still returned: it reports
// every read as a miss until the backend answers, and Watch keeps retrying.
// Returning nil there would leave caching off for the whole life of the process
// just because Redis was slow to start.
func newCache(root context.Context, cfg config.Config, logger *slog.Logger) app.Cache {
	if !cfg.CacheEnabled {
		logger.Info("cache disabled")
		return nil
	}
	store := cache.New(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB, cfg.CacheTTL, logger)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := store.Ping(ctx); err != nil {
		logger.Warn("cache unreachable at startup, serving from mysql until it comes back", "addr", cfg.RedisAddr, "error", err)
	} else if !store.Ready() {
		logger.Info("cache reachable, waiting for stale entries to expire", "addr", cfg.RedisAddr, "safety_window", (2 * cfg.CacheTTL).String())
	} else {
		logger.Info("cache enabled", "addr", cfg.RedisAddr, "ttl", cfg.CacheTTL.String())
	}
	store.Watch(root)
	return store
}

func main() {
	root, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
	store := newCache(root, cfg, logger)
	if closer, ok := store.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	repo := app.NewGORMRepository(db)
	service := app.NewServiceWithRepository(repo, store)
	notifications := app.NewNotificationHub(repo, store, logger, app.NotificationOptions{PollInterval: cfg.NotificationPollInterval, Retention: cfg.NotificationRetention})
	notifications.Start(root)
	handler := app.NewHTTPHandler(service)

	router := gin.New()
	router.Use(middleware.RequestID(), middleware.RequestLogger(logger), gin.Recovery(), cors(cfg.FrontendURL))
	app.RegisterRoutes(router, handler, authenticator.Middleware(), db)
	app.RegisterNotifications(router, notifications, authenticator.Middleware())
	app.RegisterMCP(router, service, authenticator, logger, cfg.MCPResource)
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
	server := &http.Server{Addr: net.JoinHostPort(cfg.AppHost, cfg.Port), Handler: router, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	failures := make(chan error, 1)
	go func() { failures <- server.ListenAndServe() }()
	select {
	case err := <-failures:
		if err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	case <-root.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			logger.Warn("http shutdown incomplete", "error", err)
			_ = server.Close()
		}
	}
}

func cors(allowedOrigin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && strings.EqualFold(origin, allowedOrigin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID, Idempotency-Key, Last-Event-ID")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
