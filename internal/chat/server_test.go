package chat

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testOrigin = "http://localhost:4173"

func socketClient(t *testing.T, url, token string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(url, "http")+"/api/v1/chat/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{testOrigin}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	writeFrame(t, conn, map[string]string{"type": "auth", "token": token})
	return conn
}
func writeFrame(t *testing.T, conn *websocket.Conn, value any) {
	t.Helper()
	data, _ := json.Marshal(value)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}
func readSnapshot(t *testing.T, conn *websocket.Conn, predicate func(Snapshot) bool) Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var state Snapshot
		if json.Unmarshal(data, &state) == nil && state.Type == "snapshot" && predicate(state) {
			return state
		}
	}
}
func TestTwoRealClientsSendReconnectAndActions(t *testing.T) {
	store, a, b := fixture(t)
	var secret string
	_, err := store.db.Exec("UPDATE rooms SET recovery_hash=? WHERE id=?", hash("recovery"), a.Room)
	if err != nil {
		t.Fatal(err)
	}
	secret = "recovery"
	tokenA, _ := store.Login(context.Background(), a.Room, secret, "a")
	tokenB, _ := store.Login(context.Background(), b.Room, secret, "b")
	hub := NewServer(store, []string{testOrigin})
	mux := http.NewServeMux()
	hub.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	defer hub.Close()
	ca, cb := socketClient(t, server.URL, tokenA), socketClient(t, server.URL, tokenB)
	readSnapshot(t, ca, func(s Snapshot) bool { return len(s.Messages) == 0 })
	readSnapshot(t, cb, func(s Snapshot) bool { return len(s.Messages) == 0 })
	sendA := Command{Type: "command", ID: "request-a", Action: "send", ClientID: "unique-a", Text: "hello from A"}
	writeFrame(t, ca, sendA)
	received := readSnapshot(t, cb, func(s Snapshot) bool { return len(s.Messages) == 1 })
	message := received.Messages[0].ID
	if received.Messages[0].Text != "hello from A" {
		t.Fatal("A to B delivery failed")
	}
	writeFrame(t, cb, Command{Type: "command", ID: "read", Action: "read", MessageID: message})
	readSnapshot(t, ca, func(s Snapshot) bool { return len(s.Messages) == 1 && s.Messages[0].Read })
	writeFrame(t, cb, Command{Type: "command", ID: "send-b", Action: "send", ClientID: "unique-b", Text: "reply from B", ReplyTo: message})
	readSnapshot(t, ca, func(s Snapshot) bool { return len(s.Messages) == 2 && s.Messages[1].ReplyText == "hello from A" })
	ca.CloseNow()
	ca = socketClient(t, server.URL, tokenA)
	readSnapshot(t, ca, func(s Snapshot) bool { return len(s.Messages) == 2 })
	writeFrame(t, ca, sendA)
	readSnapshot(t, ca, func(s Snapshot) bool { return len(s.Messages) == 2 })
	if len(snap(t, store, a).Messages) != 2 {
		t.Fatal("retry duplicated send")
	}
	writeFrame(t, ca, Command{Type: "command", ID: "edit", Action: "edit", MessageID: message, Text: "edited"})
	readSnapshot(t, cb, func(s Snapshot) bool {
		return len(s.Messages) == 2 && s.Messages[0].Text == "edited" && s.Messages[1].ReplyText == "edited"
	})
	writeFrame(t, ca, Command{Type: "command", ID: "pin", Action: "pin", MessageID: message, Enabled: true})
	readSnapshot(t, cb, func(s Snapshot) bool { return s.Messages[0].Pinned })
	writeFrame(t, ca, Command{Type: "command", ID: "star", Action: "star", MessageID: message, Enabled: true})
	state := readSnapshot(t, cb, func(s Snapshot) bool { return s.Messages[0].Pinned })
	if state.Messages[0].Starred {
		t.Fatal("private star broadcast to B")
	}
	writeFrame(t, ca, Command{Type: "command", ID: "revoke", Action: "revoke", MessageID: message})
	readSnapshot(t, cb, func(s Snapshot) bool {
		return s.Messages[0].Revoked && s.Messages[0].Text == "" && s.Messages[1].ReplyText == "Message unavailable"
	})
	writeFrame(t, cb, Command{Type: "command", ID: "delete", Action: "delete", MessageID: message})
	readSnapshot(t, cb, func(s Snapshot) bool { return len(s.Messages) == 1 })
	if len(snap(t, store, a).Messages) != 2 {
		t.Fatal("delete for B affected A")
	}
}
func TestWebSocketOriginAndAuthentication(t *testing.T) {
	store, _, _ := fixture(t)
	hub := NewServer(store, []string{testOrigin})
	mux := http.NewServeMux()
	hub.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	defer hub.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, res, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/chat/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"https://unknown.test"}}})
	if err == nil || res.StatusCode != 403 {
		t.Fatal("untrusted WebSocket origin accepted")
	}
	conn := socketClient(t, server.URL, "bad token")
	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("bad token accepted: %v", err)
	}
}
