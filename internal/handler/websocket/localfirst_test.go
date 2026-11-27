package websocket

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func versionPointer(v int64) *int64 { return &v }
func TestDurableCommandOutcomesAndEditConflicts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commands.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	room, _, _ := s.Create(context.Background())
	id := Identity{Room: room, Actor: "a"}
	message := apply(t, s, id, Command{ID: "send", Action: "send", ClientID: "local-send", Text: "original"})
	edit := Command{ID: "edit-one", Action: "edit", MessageID: message, Text: "first edit", ExpectedVersion: versionPointer(1)}
	first, err := s.Execute(context.Background(), id, edit)
	if err != nil || first.ErrorCode != "" {
		t.Fatalf("edit: %+v %v", first, err)
	}
	if m := snap(t, s, id).Messages[0]; m.Version != 2 || m.Text != "first edit" {
		t.Fatalf("version: %+v", m)
	}
	apply(t, s, id, Command{ID: "edit-two", Action: "edit", MessageID: message, Text: "second edit", ExpectedVersion: versionPointer(2)})
	replay, err := s.Execute(context.Background(), id, edit)
	if err != nil || replay != first {
		t.Fatalf("outcome changed: %+v / %+v %v", first, replay, err)
	}
	if m := snap(t, s, id).Messages[0]; m.Version != 3 || m.Text != "second edit" {
		t.Fatal("replayed edit overwrote newer state")
	}
	conflict := Command{ID: "stale", Action: "edit", MessageID: message, Text: "offline attempt", ExpectedVersion: versionPointer(1)}
	rejected, err := s.Execute(context.Background(), id, conflict)
	if err != nil || rejected.ErrorCode != "version_conflict" {
		t.Fatalf("stale accepted: %+v %v", rejected, err)
	}
	_ = s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	replay, err = s.Execute(context.Background(), id, conflict)
	if err != nil || replay != rejected {
		t.Fatal("rejection not retained across restart")
	}
	edit.Text = "different payload"
	if _, err = s.Apply(context.Background(), id, edit); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed operation accepted: %v", err)
	}
	apply(t, s, id, Command{ID: "reviewed", Action: "edit", MessageID: message, Text: "explicit retry", ExpectedVersion: versionPointer(3)})
}
func TestAllMutationsReplayWithoutRepeatingEffects(t *testing.T) {
	s, a, b := fixture(t)
	message := send(t, s, a, "hello")
	for _, c := range []Command{{ID: "star", Action: "star", MessageID: message, Enabled: true}, {ID: "pin", Action: "pin", MessageID: message, Enabled: true}, {ID: "revoke", Action: "revoke", MessageID: message}} {
		first, err := s.Execute(context.Background(), a, c)
		if err != nil {
			t.Fatal(err)
		}
		before := snap(t, s, a).Revision
		again, err := s.Execute(context.Background(), a, c)
		if err != nil || again != first || snap(t, s, a).Revision != before {
			t.Fatalf("mutation replayed: %s", c.Action)
		}
	}
	rejection := Command{ID: "forbidden", Action: "edit", MessageID: message, Text: "bad"}
	first, _ := s.Execute(context.Background(), b, rejection)
	again, _ := s.Execute(context.Background(), b, rejection)
	if first.ErrorCode != "forbidden" || again != first {
		t.Fatal("permission outcome not cached")
	}
}
func TestMigrationPreservesExistingHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO rooms VALUES('room','hash',9999999999999,1); INSERT INTO messages(id,room_id,sender_id,client_id,request_hash,text,created_at) VALUES('msg','room','a','client','hash','legacy',1)"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	state := snap(t, s, Identity{Room: "room", Actor: "b"})
	if len(state.Messages) != 1 || state.Messages[0].Text != "legacy" || state.Messages[0].Version != 1 {
		t.Fatal("legacy data lost")
	}
}

func TestPreRefactorCommandFingerprintStillReplays(t *testing.T) {
	s, a, _ := fixture(t)
	if _, err := s.db.Exec("INSERT INTO command_results VALUES(?,?,?,?,?)", a.Room, a.Actor, "legacy-command", "09a3379011f0286d129918e20770118fd2522053ce064a2ab877fc5e1819137b", `{"message_id":"legacy-result","revision":0}`); err != nil {
		t.Fatal(err)
	}
	result, err := s.Execute(context.Background(), a, Command{ID: "legacy-command", Type: "command", Action: "send", ClientID: "legacy-client", Text: "hello"})
	if err != nil || result.ErrorCode != "" || result.MessageID != "legacy-result" {
		t.Fatalf("legacy outcome not replayed: %+v %v", result, err)
	}
	if len(snap(t, s, a).Messages) != 0 {
		t.Fatal("replay repeated the mutation")
	}
}
