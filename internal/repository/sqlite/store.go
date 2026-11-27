// Package sqlite implements the chat persistence ports with atomic SQLite transactions.
package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	domain "github.com/okaaryanata/svaha-uni-api/internal/domain/chat"
	service "github.com/okaaryanata/svaha-uni-api/internal/service/chat"
	_ "modernc.org/sqlite"
)

//go:embed migrations/001_init.sql
var schema string

//go:embed migrations/002_command_results.sql
var commandSchema string

type Store struct {
	db *sql.DB
	mu sync.Mutex
}

func Open(path string) (store *Store, err error) {
	if path != ":memory:" {
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = db.Close()
		}
	}()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;" + schema); err != nil {
		return nil, err
	}
	var migrated int
	if err = db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=2").Scan(&migrated); err != nil {
		return nil, err
	}
	if migrated == 0 {
		tx, e := db.Begin()
		if e != nil {
			return nil, e
		}
		if _, err = tx.Exec(commandSchema); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
	}
	if path != ":memory:" {
		if err = os.Chmod(path, 0600); err != nil {
			return nil, err
		}
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) WithinTransaction(ctx context.Context, fn func(service.Transaction) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = fn(&transaction{tx: tx, ctx: ctx}); err != nil {
		return err
	}
	return tx.Commit()
}

type transaction struct {
	tx  *sql.Tx
	ctx context.Context
}

func (t *transaction) exec(q string, args ...any) error {
	_, err := t.tx.ExecContext(t.ctx, q, args...)
	return err
}
func (t *transaction) scan(q string, args []any, dest ...any) error {
	err := t.tx.QueryRowContext(t.ctx, q, args...).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}
func (t *transaction) CountRooms() (n int, err error) {
	err = t.scan("SELECT COUNT(*) FROM rooms", nil, &n)
	return
}
func (t *transaction) InsertRoom(id, hash string, now int64) error {
	return t.exec("INSERT INTO rooms(id,recovery_hash,last_active) VALUES(?,?,?)", id, hash, now)
}
func (t *transaction) Room(room, secretHash string) (revision, active int64, err error) {
	q := "SELECT revision,last_active FROM rooms WHERE id=?"
	args := []any{room}
	if secretHash != "" {
		q += " AND recovery_hash=?"
		args = append(args, secretHash)
	}
	err = t.scan(q, args, &revision, &active)
	return
}
func (t *transaction) Session(hash string) (id domain.Identity, expires, active int64, err error) {
	err = t.scan("SELECT s.room_id,s.actor_id,s.expires,r.last_active FROM sessions s JOIN rooms r ON r.id=s.room_id WHERE token_hash=?", []any{hash}, &id.Room, &id.Actor, &expires, &active)
	return
}
func (t *transaction) InsertSession(hash, room, actor string, expires int64) error {
	return t.exec("INSERT INTO sessions VALUES(?,?,?,?)", hash, room, actor, expires)
}
func (t *transaction) TrimSessions(room, actor string) error {
	return t.exec("DELETE FROM sessions WHERE room_id=? AND actor_id=? AND token_hash NOT IN (SELECT token_hash FROM sessions WHERE room_id=? AND actor_id=? ORDER BY rowid DESC LIMIT 8)", room, actor, room, actor)
}
func (t *transaction) Touch(room string, now, expires int64, revision bool) error {
	q := "UPDATE rooms SET last_active=? WHERE id=?"
	if revision {
		q = "UPDATE rooms SET revision=revision+1,last_active=? WHERE id=?"
	}
	if err := t.exec(q, now, room); err != nil {
		return err
	}
	if expires != 0 {
		return t.exec("UPDATE sessions SET expires=? WHERE room_id=?", expires, room)
	}
	return nil
}
func (t *transaction) Prune(active, expires int64) error {
	if err := t.exec("DELETE FROM rooms WHERE last_active<?", active); err != nil {
		return err
	}
	return t.exec("DELETE FROM sessions WHERE expires<?", expires)
}
func (t *transaction) Messages(id domain.Identity) (messages []domain.Message, err error) {
	messages = []domain.Message{}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT m.version,m.id,m.sender_id,m.client_id,m.text,m.reply_to,m.created_at,m.edited_at,m.revoked,m.delivered,m.read,COALESCE(p.starred,0),EXISTS(SELECT 1 FROM pins WHERE message_id=m.id AND room_id=m.room_id),CASE WHEN reply.revoked=1 OR COALESCE(rp.hidden,0)=1 THEN 'Message unavailable' ELSE COALESCE(reply.text,'') END FROM messages m LEFT JOIN preferences p ON p.message_id=m.id AND p.actor_id=? LEFT JOIN messages reply ON reply.id=m.reply_to AND reply.room_id=m.room_id LEFT JOIN preferences rp ON rp.message_id=reply.id AND rp.actor_id=? WHERE m.room_id=? AND COALESCE(p.hidden,0)=0 ORDER BY m.created_at,m.rowid`, id.Actor, id.Actor, id.Room)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var m domain.Message
		if err = rows.Scan(&m.Version, &m.ID, &m.Sender, &m.ClientID, &m.Text, &m.ReplyTo, &m.Created, &m.Edited, &m.Revoked, &m.Delivered, &m.Read, &m.Starred, &m.Pinned, &m.ReplyText); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}
func (t *transaction) Outcome(id domain.Identity, commandID string) (fingerprint string, result domain.CommandResult, err error) {
	var data string
	err = t.scan("SELECT fingerprint,result_json FROM command_results WHERE room_id=? AND actor_id=? AND command_id=?", []any{id.Room, id.Actor, commandID}, &fingerprint, &data)
	if err == nil {
		err = json.Unmarshal([]byte(data), &result)
	}
	return
}
func (t *transaction) SaveOutcome(id domain.Identity, commandID, fingerprint string, result domain.CommandResult) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return t.exec("INSERT INTO command_results VALUES(?,?,?,?,?)", id.Room, id.Actor, commandID, fingerprint, string(data))
}
func (t *transaction) Savepoint() error        { return t.exec("SAVEPOINT mutation") }
func (t *transaction) RollbackMutation() error { return t.exec("ROLLBACK TO mutation") }
func (t *transaction) Sent(id domain.Identity, clientID string) (messageID, fingerprint string, err error) {
	err = t.scan("SELECT id,request_hash FROM messages WHERE room_id=? AND sender_id=? AND client_id=?", []any{id.Room, id.Actor, clientID}, &messageID, &fingerprint)
	return
}
func (t *transaction) CountMessages(room string) (n int, err error) {
	err = t.scan("SELECT COUNT(*) FROM messages WHERE room_id=?", []any{room}, &n)
	return
}
func (t *transaction) ReplyAvailable(id domain.Identity, messageID string) (bool, error) {
	var unavailable bool
	err := t.scan("SELECT revoked OR EXISTS(SELECT 1 FROM preferences WHERE message_id=messages.id AND actor_id=? AND hidden=1) FROM messages WHERE id=? AND room_id=?", []any{id.Actor, messageID, id.Room}, &unavailable)
	return !unavailable, err
}
func (t *transaction) Message(id domain.Identity, messageID string) (m domain.Message, err error) {
	err = t.scan("SELECT sender_id,revoked,version FROM messages WHERE id=? AND room_id=?", []any{messageID, id.Room}, &m.Sender, &m.Revoked, &m.Version)
	return
}
func (t *transaction) InsertMessage(id domain.Identity, c domain.Command, messageID, fingerprint string, now int64) error {
	return t.exec("INSERT INTO messages(id,room_id,sender_id,client_id,request_hash,text,reply_to,created_at) VALUES(?,?,?,?,?,?,?,?)", messageID, id.Room, id.Actor, c.ClientID, fingerprint, c.Text, c.ReplyTo, now)
}
func (t *transaction) Edit(id, text string, now int64) error {
	return t.exec("UPDATE messages SET text=?,edited_at=?,version=version+1 WHERE id=?", text, now, id)
}
func (t *transaction) Revoke(id string) error {
	if err := t.exec("UPDATE messages SET text='',revoked=1,version=version+1 WHERE id=?", id); err != nil {
		return err
	}
	if err := t.exec("DELETE FROM pins WHERE message_id=?", id); err != nil {
		return err
	}
	return t.exec("UPDATE preferences SET starred=0 WHERE message_id=?", id)
}
func (t *transaction) Preference(id domain.Identity, messageID string, hidden, enabled bool) error {
	column := "starred"
	if hidden {
		column = "hidden"
	}
	if err := t.exec("INSERT INTO preferences(room_id,actor_id,message_id,"+column+") VALUES(?,?,?,?) ON CONFLICT(room_id,actor_id,message_id) DO UPDATE SET "+column+"=excluded."+column, id.Room, id.Actor, messageID, hidden || enabled); err != nil {
		return err
	}
	if hidden {
		return t.exec("UPDATE preferences SET starred=0 WHERE actor_id=? AND message_id=?", id.Actor, messageID)
	}
	return nil
}
func (t *transaction) Pin(room, id string, enabled bool) error {
	if enabled {
		return t.exec("INSERT OR IGNORE INTO pins VALUES(?,?)", room, id)
	}
	return t.exec("DELETE FROM pins WHERE room_id=? AND message_id=?", room, id)
}
func (t *transaction) Receipt(id domain.Identity, messageID string, read bool) error {
	q := "UPDATE messages SET delivered=1 WHERE room_id=? AND sender_id!=? AND rowid <= (SELECT rowid FROM messages WHERE id=?)"
	if read {
		q = "UPDATE messages SET delivered=1,read=1 WHERE room_id=? AND sender_id!=? AND rowid <= (SELECT rowid FROM messages WHERE id=?)"
	}
	return t.exec(q, id.Room, id.Actor, messageID)
}

var _ service.Repository = (*Store)(nil)
var _ service.Transaction = (*transaction)(nil)
