package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/okaaryanata/svaha-uni-api/internal/domain/sudoku"
	chat "github.com/okaaryanata/svaha-uni-api/internal/handler/websocket"
	sudokuservice "github.com/okaaryanata/svaha-uni-api/internal/service/sudoku"
)

type GIFProvider interface {
	GIF(context.Context) ([]byte, error)
}

func New(provider GIFProvider, origins []string, chats ...*chat.Server) http.Handler {
	mux := http.NewServeMux()
	for _, service := range chats {
		service.Register(mux)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /api/v1/sudoku/solve", func(w http.ResponseWriter, r *http.Request) {
		if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
			fail(w, 415, "content_type", "use application/json")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var request struct {
			Board [][]int `json:"board"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			fail(w, 400, "invalid_json", "send a JSON object containing board")
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			fail(w, 400, "invalid_json", "send exactly one JSON object")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		board, err := sudokuservice.New().Solve(ctx, request.Board)
		switch {
		case errors.Is(err, sudoku.ErrInvalid):
			fail(w, 422, "invalid_board", err.Error())
		case errors.Is(err, sudoku.ErrUnsolvable):
			fail(w, 422, "unsolvable", err.Error())
		case err != nil:
			fail(w, 408, "solve_timeout", "puzzle solving was cancelled or exceeded the time limit")
		default:
			writeJSON(w, 200, map[string]any{"board": board})
		}
	})
	mux.HandleFunc("GET /api/v1/cats/gif", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		data, err := provider.GIF(ctx)
		if err != nil {
			slog.Warn("cat provider request failed", "error", err)
			fail(w, 502, "cat_provider_unavailable", "unable to load a cat GIF; please try again")
			return
		}
		w.Header().Set("Content-Type", "image/gif")
		w.Header().Set("Cache-Control", "no-store")
		if _, err := w.Write(data); err != nil {
			slog.Warn("write GIF response", "error", err)
		}
	})
	allowed := make(map[string]bool)
	for _, origin := range origins {
		if origin = strings.TrimSpace(origin); origin != "" {
			allowed[origin] = true
		}
	}
	// Bound simultaneous expensive work. Extra callers receive a retryable response.
	slots := make(chan struct{}, 16)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Add("Vary", "Origin")
		origin := r.Header.Get("Origin")
		if origin != "" && allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			if !allowed[origin] {
				fail(w, 403, "origin_not_allowed", "origin is not allowed")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path != "/healthz" && r.URL.Path != "/api/v1/chat/ws" {
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			default:
				w.Header().Set("Retry-After", "1")
				fail(w, 503, "busy", "server busy; retry shortly")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Warn("write JSON response", "error", err)
	}
}
func fail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
