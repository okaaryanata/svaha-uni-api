package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	cats "github.com/okaaryanata/svaha-uni-api/internal/client/cataas"
	httpapi "github.com/okaaryanata/svaha-uni-api/internal/handler/http"
	ws "github.com/okaaryanata/svaha-uni-api/internal/handler/websocket"
	repo "github.com/okaaryanata/svaha-uni-api/internal/repository/sqlite"
	catservice "github.com/okaaryanata/svaha-uni-api/internal/service/cats"
	"github.com/okaaryanata/svaha-uni-api/internal/service/chat"
)

func Run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 8
	client := &http.Client{Timeout: 12 * time.Second, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	origins := strings.Split(env("CORS_ORIGINS", "http://127.0.0.1:4173,http://localhost:4173,http://127.0.0.1:4174,http://localhost:4174"), ",")
	store, err := repo.Open(env("DATABASE_PATH", "data/chat.db"))
	if err != nil {
		return fmt.Errorf("open chat database: %w", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			slog.Warn("close database", "error", err)
		}
	}()
	service := chat.New(store, time.Now)
	if err := service.Prune(context.Background()); err != nil {
		return fmt.Errorf("cleanup expired demos: %w", err)
	}
	chats := ws.NewServer(service, origins)
	defer func() { chats.Close() }()
	provider := cats.Client{HTTP: client, URL: "https://cataas.com/cat/gif"}
	server := &http.Server{Addr: env("HTTP_ADDR", ":8081"), Handler: httpapi.New(catservice.New(provider), origins, chats), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
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
				if err := service.Prune(ctx); err != nil && ctx.Err() == nil {
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
			return fmt.Errorf("server stopped: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	chats.Close()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		slog.Error("shutdown failed", "error", err)
		_ = server.Close()
		return err
	}
	return nil
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
