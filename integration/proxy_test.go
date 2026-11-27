package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// This opt-in test exercises the deployed/dev frontend proxy, not just the hub.
// API_SMOKE_URL=http://127.0.0.1:4173 go test ./integration -v
func TestFrontendProxy(t *testing.T) {
	base := os.Getenv("API_SMOKE_URL")
	if base == "" {
		t.Skip("set API_SMOKE_URL with the frontend and API running")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	post := func(path string, body any) map[string]any {
		t.Helper()
		data, _ := json.Marshal(body)
		res, err := client.Post(base+path, "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode >= 300 {
			t.Fatalf("%s: %s", path, res.Status)
		}
		var result map[string]any
		if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	demo := post("/api/v1/chat/demos", map[string]any{})
	connect := func(actor string) *websocket.Conn {
		t.Helper()
		session := post("/api/v1/chat/sessions", map[string]any{"room_id": demo["room_id"], "recovery_token": demo["recovery_token"], "account_id": actor})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/api/v1/chat/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"http://127.0.0.1:4173"}}}) //nolint:bodyclose // coder/websocket owns and closes the handshake body.
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		payload, _ := json.Marshal(map[string]any{"type": "auth", "token": session["token"]})
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.Read(ctx); err != nil {
			t.Fatal(err)
		}
		return conn
	}
	a, b := connect("a"), connect("b")
	send := func(conn *websocket.Conn, id, text string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		payload, _ := json.Marshal(map[string]any{"type": "command", "id": id, "action": "send", "client_message_id": id, "text": text})
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			t.Fatal(err)
		}
	}
	receive := func(conn *websocket.Conn, wanted string, count int) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var event struct {
				Type     string
				Messages []struct{ Text string }
			}
			if err := json.Unmarshal(data, &event); err != nil {
				t.Fatal(err)
			}
			if event.Type != "snapshot" || len(event.Messages) != count {
				continue
			}
			for _, message := range event.Messages {
				if message.Text == wanted {
					return
				}
			}
		}
	}
	send(a, "a-message", "hello through the frontend proxy")
	receive(b, "hello through the frontend proxy", 1)
	send(b, "b-message", "response through the frontend proxy")
	receive(a, "response through the frontend proxy", 2)
	send(a, "a-message", "hello through the frontend proxy")
	receive(a, "response through the frontend proxy", 2)
}
