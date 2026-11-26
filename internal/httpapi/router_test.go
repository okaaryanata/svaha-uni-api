package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeCats struct{ err error }

func (f fakeCats) GIF(context.Context) ([]byte, error) { return []byte("GIF89aTEST"), f.err }
func TestRoutes(t *testing.T) {
	handler := New(fakeCats{}, []string{"http://localhost:4173"})
	for _, tt := range []struct {
		method, path, body string
		status             int
	}{{"GET", "/healthz", "", 200}, {"GET", "/api/v1/cats/gif", "", 200}, {"POST", "/api/v1/sudoku/solve", `{"board":[]}`, 422}, {"POST", "/api/v1/sudoku/solve", `{"board":[[0]]}`, 422}, {"POST", "/api/v1/sudoku/solve", `{} {}`, 400}, {"POST", "/api/v1/sudoku/solve", `{"extra":1}`, 400}, {"GET", "/api/v1/sudoku/solve", "", 405}, {"GET", "/missing", "", 404}} {
		t.Run(tt.method+tt.path+tt.body, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tt.status {
				t.Fatalf("status %d: %s", res.Code, res.Body.String())
			}
		})
	}
}
func TestSolveAPI(t *testing.T) {
	board := make([][]int, 9)
	for i := range board {
		board[i] = make([]int, 9)
	}
	body, _ := json.Marshal(map[string]any{"board": board})
	req := httptest.NewRequest("POST", "/api/v1/sudoku/solve", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	New(fakeCats{}, nil).ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	var response struct{ Board [9][9]int }
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil || response.Board[8][8] == 0 {
		t.Fatal("missing solved board")
	}
}
func TestCORS(t *testing.T) {
	handler := New(fakeCats{}, []string{"http://localhost:4173"})
	for _, origin := range []string{"http://localhost:4173", "https://unknown.test"} {
		req := httptest.NewRequest("OPTIONS", "/api/v1/sudoku/solve", nil)
		req.Header.Set("Origin", origin)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if origin == "http://localhost:4173" {
			if res.Code != 204 || res.Header().Get("Access-Control-Allow-Origin") != origin {
				t.Fatal("allowed origin failed")
			}
		} else if res.Code != 403 || res.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("unexpected CORS grant")
		}
	}
}
func TestProviderFailure(t *testing.T) {
	res := httptest.NewRecorder()
	New(fakeCats{errors.New("offline")}, nil).ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/cats/gif", nil))
	if res.Code != 502 {
		t.Fatal(res.Code)
	}
}
