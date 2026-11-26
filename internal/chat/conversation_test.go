package chat

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestConversationSummaryUpdatesPerAccount(t *testing.T) {
	s, a, b := fixture(t)
	if c := snap(t, s, b).Conversation; c.Peer.ID != "a" || c.Peer.Name != "Demo A" || c.LastMessage != nil || c.UnreadCount != 0 {
		t.Fatalf("empty summary: %+v", c)
	}
	first := send(t, s, a, "first")
	second := send(t, s, a, "second")
	c := snap(t, s, b).Conversation
	if c.UnreadCount != 2 || c.LastMessage.ID != second || c.LastMessage.Text != "second" {
		t.Fatalf("received summary: %+v", c)
	}
	created := c.LastMessage.Created
	apply(t, s, a, Command{Action: "edit", MessageID: second, Text: "second edited"})
	c = snap(t, s, b).Conversation
	if c.LastMessage.Text != "second edited" || c.LastMessage.Created != created || c.UnreadCount != 2 {
		t.Fatalf("edit summary: %+v", c)
	}
	apply(t, s, b, Command{Action: "delivered", MessageID: second})
	if snap(t, s, b).Conversation.UnreadCount != 2 {
		t.Fatal("delivery cleared unread")
	}
	apply(t, s, b, Command{Action: "read", MessageID: second})
	apply(t, s, a, Command{Action: "edit", MessageID: second, Text: "read then edited"})
	if snap(t, s, b).Conversation.UnreadCount != 0 {
		t.Fatal("edit created unread activity")
	}
	apply(t, s, a, Command{Action: "pin", MessageID: first, Enabled: true})
	apply(t, s, b, Command{Action: "star", MessageID: first, Enabled: true})
	if snap(t, s, b).Conversation.LastMessage.ID != second {
		t.Fatal("pin/star reordered preview")
	}
	third := send(t, s, a, "third")
	apply(t, s, a, Command{Action: "revoke", MessageID: third})
	c = snap(t, s, b).Conversation
	if c.UnreadCount != 0 || !c.LastMessage.Revoked || c.LastMessage.Text != "" || c.LastMessage.ID != third {
		t.Fatalf("revoke summary: %+v", c)
	}
	apply(t, s, b, Command{Action: "delete", MessageID: third})
	if snap(t, s, b).Conversation.LastMessage.ID != second || snap(t, s, a).Conversation.LastMessage.ID != third {
		t.Fatal("personal delete did not filter preview independently")
	}
	apply(t, s, b, Command{Action: "delete", MessageID: second})
	apply(t, s, b, Command{Action: "delete", MessageID: first})
	if c = snap(t, s, b).Conversation; c.LastMessage != nil || c.UnreadCount != 0 {
		t.Fatal("empty filtered history retained preview")
	}
}
func TestDeletingUnreadMessageClearsOnlyThatAccountsUnread(t *testing.T) {
	s, a, b := fixture(t)
	id := send(t, s, a, "unread")
	apply(t, s, b, Command{Action: "delete", MessageID: id})
	if snap(t, s, b).Conversation.UnreadCount != 0 || snap(t, s, b).Conversation.LastMessage != nil {
		t.Fatal("hidden message counted")
	}
	if len(snap(t, s, a).Messages) != 1 || snap(t, s, a).Messages[0].Read {
		t.Fatal("personal delete changed sender history or receipt")
	}
}
func readPresence(t *testing.T, conn *websocket.Conn, wantB bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var event struct {
			Type     string
			Accounts map[string]bool
		}
		if json.Unmarshal(data, &event) == nil && event.Type == "presence" {
			if event.Accounts["b"] != wantB {
				t.Fatalf("wanted B online=%v, got %s", wantB, data)
			}
			return
		}
	}
}
func setupPresence(t *testing.T) (*Store, *Server, *httptest.Server, string, string) {
	t.Helper()
	store, a, _ := fixture(t)
	store.db.Exec("UPDATE rooms SET recovery_hash=? WHERE id=?", hash("recovery"), a.Room)
	ta, _ := store.Login(context.Background(), a.Room, "recovery", "a")
	tb, _ := store.Login(context.Background(), a.Room, "recovery", "b")
	hub := NewServer(store, []string{testOrigin})
	mux := http.NewServeMux()
	hub.Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(func() { hub.Close(); server.Close() })
	return store, hub, server, ta, tb
}
func TestPresenceTracksLastConnection(t *testing.T) {
	_, _, server, ta, tb := setupPresence(t)
	a := socketClient(t, server.URL, ta)
	readSnapshot(t, a, func(Snapshot) bool { return true })
	readPresence(t, a, false)
	b1 := socketClient(t, server.URL, tb)
	readSnapshot(t, b1, func(Snapshot) bool { return true })
	readPresence(t, a, true)
	b2 := socketClient(t, server.URL, tb)
	readSnapshot(t, b2, func(Snapshot) bool { return true })
	readPresence(t, a, true)
	b1.CloseNow()
	readPresence(t, a, true)
	b2.CloseNow()
	readPresence(t, a, false)
}
func TestPresenceDetectsFailedHeartbeat(t *testing.T) {
	_, hub, server, ta, tb := setupPresence(t)
	hub.heartbeatEvery = 30 * time.Millisecond
	hub.heartbeatTimeout = 80 * time.Millisecond
	a := socketClient(t, server.URL, ta)
	readSnapshot(t, a, func(Snapshot) bool { return true })
	readPresence(t, a, false)
	b := socketClient(t, server.URL, tb)
	readSnapshot(t, b, func(Snapshot) bool { return true })
	readPresence(t, a, true) /* B never reads again, so cannot answer ping frames. */
	readPresence(t, a, false)
}
func TestPresenceDoesNotLeakAcrossRooms(t *testing.T) {
	s, _, _ := fixture(t)
	hub := NewServer(s, nil)
	a := &peer{id: Identity{"one", "a"}, queue: make(chan any, 4), cancel: func() {}}
	b := &peer{id: Identity{"two", "b"}, queue: make(chan any, 4), cancel: func() {}}
	hub.peers[a] = true
	hub.peers[b] = true
	hub.presenceLocked("one")
	if len(a.queue) != 1 || len(b.queue) != 0 {
		t.Fatal("presence leaked into another room")
	}
}
func TestOfflineDeliverySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "offline.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	room, secret, err := store.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ta, _ := store.Login(context.Background(), room, secret, "a")
	tb, _ := store.Login(context.Background(), room, secret, "b")
	start := func(s *Store) (*Server, *httptest.Server) {
		hub := NewServer(s, []string{testOrigin})
		mux := http.NewServeMux()
		hub.Register(mux)
		return hub, httptest.NewServer(mux)
	}
	hub, server := start(store)
	a := socketClient(t, server.URL, ta)
	readSnapshot(t, a, func(Snapshot) bool { return true })
	ids := []string{}
	for i, text := range []string{"offline one", "offline two", "offline three"} {
		command := Command{Type: "command", ID: randomID(), Action: "send", ClientID: randomID(), Text: text}
		writeFrame(t, a, command)
		state := readSnapshot(t, a, func(s Snapshot) bool { return len(s.Messages) == i+1 })
		for _, m := range state.Messages {
			if m.Delivered || m.Read {
				t.Fatal("offline recipient marked delivered/read")
			}
		}
		ids = append(ids, state.Messages[i].ID)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		for {
			_, data, e := a.Read(ctx)
			if e != nil {
				cancel()
				t.Fatal(e)
			}
			var ack struct{ Type, ID string }
			json.Unmarshal(data, &ack)
			if ack.Type == "ack" && ack.ID == command.ID {
				break
			}
		}
		cancel()
	}
	apply(t, store, Identity{room, "a"}, Command{Action: "edit", MessageID: ids[0], Text: "offline edited"})
	apply(t, store, Identity{room, "a"}, Command{Action: "revoke", MessageID: ids[1]})
	a.CloseNow()
	hub.Close()
	server.Close()
	store.Close()
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hub, server = start(store)
	defer server.Close()
	defer hub.Close()
	b := socketClient(t, server.URL, tb)
	state := readSnapshot(t, b, func(Snapshot) bool { return true })
	if len(state.Messages) != 3 || state.Messages[0].Text != "offline edited" || !state.Messages[1].Revoked || state.Messages[1].Text != "" || state.Conversation.UnreadCount != 2 || state.Conversation.LastMessage.ID != ids[2] {
		t.Fatalf("offline history not recovered: %+v", state)
	}
	a = socketClient(t, server.URL, ta)
	readSnapshot(t, a, func(Snapshot) bool { return true })
	writeFrame(t, b, Command{Type: "command", ID: randomID(), Action: "delivered", MessageID: ids[2]})
	readSnapshot(t, a, func(s Snapshot) bool { return s.Messages[2].Delivered && !s.Messages[2].Read })
	state = readSnapshot(t, b, func(s Snapshot) bool { return s.Messages[2].Delivered })
	if state.Conversation.UnreadCount != 2 {
		t.Fatal("reconnect incorrectly marked history read")
	}
	writeFrame(t, b, Command{Type: "command", ID: randomID(), Action: "read", MessageID: ids[2]})
	readSnapshot(t, b, func(s Snapshot) bool { return s.Conversation.UnreadCount == 0 })
	readSnapshot(t, a, func(s Snapshot) bool { return s.Messages[2].Read })
}
