package chat

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Store, Identity, Identity) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	room, _, err := s.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s, Identity{room, "a"}, Identity{room, "b"}
}
func apply(t *testing.T, s *Store, id Identity, c Command) string {
	t.Helper()
	if c.ID == "" {
		c.ID = randomID()
	}
	message, err := s.Apply(context.Background(), id, c)
	if err != nil {
		t.Fatal(err)
	}
	return message
}
func snap(t *testing.T, s *Store, id Identity) Snapshot {
	t.Helper()
	state, err := s.Snapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return state
}
func send(t *testing.T, s *Store, id Identity, text string) string {
	t.Helper()
	return apply(t, s, id, Command{Action: "send", ClientID: randomID(), Text: text})
}
func TestPrivateActionsAndSharedPins(t *testing.T) {
	s, a, b := fixture(t)
	message := send(t, s, a, "hello")
	apply(t, s, a, Command{Action: "star", MessageID: message, Enabled: true})
	if !snap(t, s, a).Messages[0].Starred || snap(t, s, b).Messages[0].Starred {
		t.Fatal("star leaked")
	}
	apply(t, s, b, Command{Action: "pin", MessageID: message, Enabled: true})
	if !snap(t, s, a).Messages[0].Pinned {
		t.Fatal("pin not shared")
	}
	apply(t, s, a, Command{Action: "delete", MessageID: message})
	if len(snap(t, s, a).Messages) != 0 || len(snap(t, s, b).Messages) != 1 {
		t.Fatal("delete affected peer")
	}
}
func TestReplyEditRevokeAndPermission(t *testing.T) {
	s, a, b := fixture(t)
	message := send(t, s, a, "hello")
	reply := apply(t, s, b, Command{Action: "send", ClientID: randomID(), Text: "reply", ReplyTo: message})
	_ = reply
	for _, action := range []string{"edit", "revoke"} {
		if _, err := s.Apply(context.Background(), b, Command{ID: randomID(), Action: action, MessageID: message, Text: "forged"}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("unauthorized %s: %v", action, err)
		}
	}
	apply(t, s, a, Command{Action: "edit", MessageID: message, Text: "updated"})
	if snap(t, s, b).Messages[1].ReplyText != "updated" {
		t.Fatal("reply not updated")
	}
	apply(t, s, a, Command{Action: "pin", MessageID: message, Enabled: true})
	apply(t, s, b, Command{Action: "star", MessageID: message, Enabled: true})
	apply(t, s, a, Command{Action: "revoke", MessageID: message})
	state := snap(t, s, b)
	if state.Messages[0].Text != "" || !state.Messages[0].Revoked || state.Messages[0].Pinned || state.Messages[0].Starred || state.Messages[1].ReplyText != "Message unavailable" {
		t.Fatalf("revoke leaked content: %+v", state)
	}
	if _, err := s.Apply(context.Background(), a, Command{ID: randomID(), Action: "edit", MessageID: message, Text: "resurrect"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("edited revoked message")
	}
}
func TestConcurrentDuplicateAndRetryAfterEdit(t *testing.T) {
	s, a, _ := fixture(t)
	command := Command{ID: "command", Action: "send", ClientID: "same-client-id", Text: "one"}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	ids := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); id, err := s.Apply(context.Background(), a, command); errs <- err; ids <- id }()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first string
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("duplicate got different ID")
		}
	}
	if len(snap(t, s, a).Messages) != 1 {
		t.Fatal("duplicate rows")
	}
	apply(t, s, a, Command{Action: "edit", MessageID: first, Text: "edited"})
	if got := apply(t, s, a, command); got != first {
		t.Fatal("retry did not resolve original ID")
	}
	command.Text = "changed send"
	if _, err := s.Apply(context.Background(), a, command); !errors.Is(err, ErrConflict) {
		t.Fatalf("wanted conflict: %v", err)
	}
}
func TestIsolationAndReceipts(t *testing.T) {
	s, a, b := fixture(t)
	first := send(t, s, a, "first")
	second := send(t, s, a, "second")
	apply(t, s, b, Command{Action: "read", MessageID: second})
	for _, message := range snap(t, s, a).Messages {
		if !message.Read || !message.Delivered {
			t.Fatal("receipt not advanced")
		}
	}
	room, _, _ := s.Create(context.Background())
	other := Identity{room, "a"}
	for _, action := range []string{"edit", "revoke", "delete", "star", "pin", "read"} {
		if _, err := s.Apply(context.Background(), other, Command{ID: randomID(), Action: action, MessageID: first, Text: "bad", Enabled: true}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("cross-room %s: %v", action, err)
		}
	}
	if len(snap(t, s, other).Messages) != 0 {
		t.Fatal("room leaked")
	}
}
func TestHiddenReplyPreview(t *testing.T) {
	s, a, b := fixture(t)
	first := send(t, s, a, "private after hide")
	apply(t, s, b, Command{Action: "send", ClientID: randomID(), Text: "response", ReplyTo: first})
	apply(t, s, b, Command{Action: "delete", MessageID: first})
	if snap(t, s, b).Messages[0].ReplyText != "Message unavailable" {
		t.Fatal("hidden source leaked through reply")
	}
	if snap(t, s, a).Messages[1].ReplyText != "private after hide" {
		t.Fatal("peer visibility affected")
	}
}
func TestPersistenceSessionsAndExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	room, secret, _ := s.Create(context.Background())
	token, err := s.Login(context.Background(), room, secret, "a")
	if err != nil {
		t.Fatal(err)
	}
	a := Identity{room, "a"}
	message := send(t, s, a, "persistent")
	apply(t, s, a, Command{Action: "star", MessageID: message, Enabled: true})
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if id, err := s.Authenticate(context.Background(), token); err != nil || id != a {
		t.Fatalf("session lost %v", err)
	}
	if !snap(t, s, a).Messages[0].Starred {
		t.Fatal("state lost")
	}
	if _, err := s.Login(context.Background(), room, "wrong", "b"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("invalid room credential accepted")
	}
	s.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	if _, err := s.Authenticate(context.Background(), token); !errors.Is(err, ErrExpired) {
		t.Fatal("session did not expire")
	}
	if err := s.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	if count != 0 {
		t.Fatal("cleanup did not cascade")
	}
}
func TestValidationAndSessionBounds(t *testing.T) {
	s, a, _ := fixture(t)
	for _, c := range []Command{{Action: "send", ClientID: "x", Text: " "}, {Action: "send", Text: "hello"}, {Action: "send", ClientID: "x", Text: string(make([]byte, 2001))}, {Action: "send", ClientID: "x", Text: "reply", ReplyTo: "missing"}} {
		c.ID = randomID()
		if _, err := s.Apply(context.Background(), a, c); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid command accepted: %v", err)
		}
	}
	room, secret, _ := s.Create(context.Background())
	for i := 0; i < 12; i++ {
		if _, err := s.Login(context.Background(), room, secret, "a"); err != nil {
			t.Fatal(err)
		}
	}
	var sessions int
	s.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE room_id=?", room).Scan(&sessions)
	if sessions != 8 {
		t.Fatalf("sessions not bounded: %d", sessions)
	}
}
