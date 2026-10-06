package main

import (
	"context"
	"errors"
	"github.com/redis/go-redis/v9"
	"github.com/shawns-yao/Sideria/internal/server"
	"github.com/shawns-yao/Sideria/web"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	token := os.Getenv("SIDERIA_ADMIN_TOKEN")
	if len(token) < 32 {
		slog.Error("SIDERIA_ADMIN_TOKEN must be at least 32 characters")
		os.Exit(1)
	}
	store, err := server.OpenStore(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		slog.Error("database initialization failed")
		os.Exit(1)
	}
	defer store.Pool.Close()
	opts, err := redis.ParseURL(os.Getenv("REDIS_URL"))
	if err != nil {
		slog.Error("invalid REDIS_URL")
		os.Exit(1)
	}
	r := redis.NewClient(opts)
	defer r.Close()
	dir := os.Getenv("SIDERIA_TRANSFER_DIR")
	if dir == "" {
		dir = ".run/transfers"
	}
	if os.MkdirAll(dir, 0700) != nil {
		slog.Error("transfer directory unavailable")
		os.Exit(1)
	}
	dev := os.Getenv("SIDERIA_DEV") == "1"
	s := server.New(store, r, server.Config{AdminToken: token, Dev: dev, TransferDir: dir, AIURL: os.Getenv("SIDERIA_AI_URL"), AIKey: os.Getenv("SIDERIA_AI_KEY"), AIModel: os.Getenv("SIDERIA_AI_MODEL"), AIAllow: os.Getenv("SIDERIA_AI_ALLOW_EGRESS") == "1"})
	go s.Run(ctx)
	addr := os.Getenv("SIDERIA_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	httpServer := &http.Server{Addr: addr, Handler: s.Handler(web.Assets()), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Minute, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		httpServer.Shutdown(c)
	}()
	slog.Info("Sideria starting", "address", addr, "development", dev)
	if dev {
		err = httpServer.ListenAndServe()
	} else {
		err = httpServer.ListenAndServeTLS(os.Getenv("SIDERIA_TLS_CERT"), os.Getenv("SIDERIA_TLS_KEY"))
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("HTTP service failed", "error", err)
		os.Exit(1)
	}
}
