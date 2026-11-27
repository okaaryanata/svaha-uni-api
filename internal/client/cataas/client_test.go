package cataas

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestGIF(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		status     int
		ok         bool
	}{{"valid", "GIF89aDATA", 200, true}, {"upstream error", "down", 503, false}, {"wrong type", "<html>oops", 200, false}, {"too large", "GIF89a" + strings.Repeat("x", MaxGIFBytes), 200, false}} {
		t.Run(tt.name, func(t *testing.T) {
			client := Client{URL: "https://example.test/cat/gif", HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Accept") != "image/gif" {
					t.Error("missing accept")
				}
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body))}, nil
			})}}
			_, err := client.GIF(context.Background())
			if (err == nil) != tt.ok {
				t.Fatalf("got %v", err)
			}
		})
	}
}
