package main

import (
	"context"
	"errors"
	"github.com/okaaryanata/oka-portfolio-api/internal/cats"
	"github.com/okaaryanata/oka-portfolio-api/internal/chat"
	"github.com/okaaryanata/oka-portfolio-api/internal/httpapi"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 8
	client := &http.Client{Timeout: 12 * time.Second, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	origins := strings.Split(env("CORS_ORIGINS", "http://127.0.0.1:4173,http://localhost:4173,http://127.0.0.1:4174,http://localhost:4174"), ",")
	store, err := chat.Open(env("DATABASE_PATH", "data/chat.db"))
	if err != nil {
		slog.Error("open chat database", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	if err := store.Prune(context.Background()); err != nil {
		slog.Error("cleanup expired demos", "error", err)
		os.Exit(1)
	}
	chats := chat.NewServer(store, origins)
	defer chats.Close()
	provider := cats.Client{HTTP: client, URL: "https://cataas.com/cat/gif"}
	server := &http.Server{Addr: env("HTTP_ADDR", ":8081"), Handler: httpapi.New(provider, origins, chats), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := store.Prune(ctx); err != nil && ctx.Err() == nil {
					slog.Warn("cleanup demos", "error", err)
				}
			}
		}
	}()
	failure := make(chan error, 1)
	go func() { slog.Info("API listening", "address", server.Addr); failure <- server.ListenAndServe() }()
	select {
	case err := <-failure:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server stopped", "error", err)
			os.Exit(1)
		}
		return
	case <-ctx.Done():
	}
	chats.Close()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		slog.Error("shutdown failed", "error", err)
		server.Close()
	}
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
