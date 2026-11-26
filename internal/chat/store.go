package chat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"sync"
	"time"
)

//go:embed migrations/001_init.sql
var schema string

//go:embed migrations/002_command_results.sql
var commandSchema string

const retention = 24 * time.Hour

var ErrExpired = errors.New("demo expired; start a new demo")
var ErrUnauthorized = errors.New("invalid demo session")
var ErrInvalid = errors.New("invalid chat command")
var ErrForbidden = errors.New("message action is not allowed")
var ErrVersionConflict = errors.New("message changed on the server; review your edit")
var ErrConflict = errors.New("message ID was already used with different content")

type Identity struct {
	Room  string
	Actor string
}
type Message struct {
	Version   int64  `json:"version"`
	ID        string `json:"id"`
	Sender    string `json:"sender_id"`
	ClientID  string `json:"client_message_id"`
	Text      string `json:"text"`
	ReplyTo   string `json:"reply_to,omitempty"`
	ReplyText string `json:"reply_text,omitempty"`
	Created   int64  `json:"created_at"`
	Edited    int64  `json:"edited_at,omitempty"`
	Revoked   bool   `json:"revoked"`
	Delivered bool   `json:"delivered"`
	Read      bool   `json:"read"`
	Starred   bool   `json:"starred"`
	Pinned    bool   `json:"pinned"`
}
type ConversationPeer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type MessagePreview struct {
	ID      string `json:"id"`
	Sender  string `json:"sender_id"`
	Text    string `json:"text"`
	Created int64  `json:"created_at"`
	Edited  int64  `json:"edited_at,omitempty"`
	Revoked bool   `json:"revoked"`
}
type Conversation struct {
	Peer        ConversationPeer `json:"peer"`
	UnreadCount int              `json:"unread_count"`
	LastMessage *MessagePreview  `json:"last_message"`
}

func conversationFor(messages []Message, actor string) Conversation {
	peerID, peerName := "a", "Demo A"
	if actor == "a" {
		peerID, peerName = "b", "Demo B"
	}
	result := Conversation{Peer: ConversationPeer{ID: peerID, Name: peerName}}
	for _, message := range messages {
		if message.Sender != actor && !message.Read && !message.Revoked {
			result.UnreadCount++
		}
		result.LastMessage = &MessagePreview{ID: message.ID, Sender: message.Sender, Text: message.Text, Created: message.Created, Edited: message.Edited, Revoked: message.Revoked}
	}
	return result
}

type Snapshot struct {
	Type         string       `json:"type"`
	Revision     int64        `json:"revision"`
	Messages     []Message    `json:"messages"`
	Conversation Conversation `json:"conversation"`
}
type Store struct {
	db  *sql.DB
	mu  sync.Mutex
	now func() time.Time
}

func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;" + schema); err != nil {
		db.Close()
		return nil, err
	}
	var migrated int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=2").Scan(&migrated); err != nil {
		db.Close()
		return nil, err
	}
	if migrated == 0 {
		tx, err := db.Begin()
		if err != nil {
			db.Close()
			return nil, err
		}
		if _, err = tx.Exec(commandSchema); err != nil {
			tx.Rollback()
			db.Close()
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			db.Close()
			return nil, err
		}
	}
	if path != ":memory:" {
		if err := os.Chmod(path, 0600); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db, now: time.Now}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func randomID() string {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes[:])
}
func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func (s *Store) Create(ctx context.Context) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM rooms").Scan(&count); err != nil {
		return "", "", err
	}
	if count >= 1000 {
		return "", "", errors.New("demo room capacity reached; retry later")
	}
	id, secret := randomID(), randomID()
	_, err := s.db.ExecContext(ctx, "INSERT INTO rooms(id,recovery_hash,last_active) VALUES(?,?,?)", id, hash(secret), s.now().UnixMilli())
	return id, secret, err
}
func (s *Store) Login(ctx context.Context, room, secret, actor string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actor != "a" && actor != "b" {
		return "", ErrUnauthorized
	}
	var activity int64
	err := s.db.QueryRowContext(ctx, "SELECT last_active FROM rooms WHERE id=? AND recovery_hash=?", room, hash(secret)).Scan(&activity)
	if err == sql.ErrNoRows {
		return "", ErrUnauthorized
	}
	if err != nil {
		return "", err
	}
	if activity < s.now().Add(-retention).UnixMilli() {
		return "", ErrExpired
	}
	token := randomID()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, "INSERT INTO sessions VALUES(?,?,?,?)", hash(token), room, actor, s.now().Add(retention).UnixMilli())
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE room_id=? AND actor_id=? AND token_hash NOT IN (SELECT token_hash FROM sessions WHERE room_id=? AND actor_id=? ORDER BY rowid DESC LIMIT 8)", room, actor, room, actor)
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, "UPDATE rooms SET last_active=? WHERE id=?", s.now().UnixMilli(), room)
	if err != nil {
		return "", err
	}
	return token, tx.Commit()
}
func (s *Store) Authenticate(ctx context.Context, token string) (Identity, error) {
	var id Identity
	var expires, active int64
	err := s.db.QueryRowContext(ctx, "SELECT s.room_id,s.actor_id,s.expires,r.last_active FROM sessions s JOIN rooms r ON r.id=s.room_id WHERE token_hash=?", hash(token)).Scan(&id.Room, &id.Actor, &expires, &active)
	if err == sql.ErrNoRows {
		return id, ErrUnauthorized
	}
	if err != nil {
		return id, err
	}
	if expires < s.now().UnixMilli() || active < s.now().Add(-retention).UnixMilli() {
		return id, ErrExpired
	}
	return id, nil
}
func (s *Store) Prune(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, "DELETE FROM rooms WHERE last_active<?", s.now().Add(-retention).UnixMilli())
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires<?", s.now().UnixMilli())
	return err
}
func (s *Store) Snapshot(ctx context.Context, id Identity) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result Snapshot
	result.Type = "snapshot"
	result.Messages = []Message{}
	var active int64
	if err := s.db.QueryRowContext(ctx, "SELECT revision,last_active FROM rooms WHERE id=?", id.Room).Scan(&result.Revision, &active); err != nil {
		return result, ErrExpired
	}
	if active < s.now().Add(-retention).UnixMilli() {
		return result, ErrExpired
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.version,m.id,m.sender_id,m.client_id,m.text,m.reply_to,m.created_at,m.edited_at,m.revoked,m.delivered,m.read,COALESCE(p.starred,0),EXISTS(SELECT 1 FROM pins WHERE message_id=m.id AND room_id=m.room_id),CASE WHEN reply.revoked=1 OR COALESCE(rp.hidden,0)=1 THEN 'Message unavailable' ELSE COALESCE(reply.text,'') END FROM messages m LEFT JOIN preferences p ON p.message_id=m.id AND p.actor_id=? LEFT JOIN messages reply ON reply.id=m.reply_to AND reply.room_id=m.room_id LEFT JOIN preferences rp ON rp.message_id=reply.id AND rp.actor_id=? WHERE m.room_id=? AND COALESCE(p.hidden,0)=0 ORDER BY m.created_at,m.rowid`, id.Actor, id.Actor, id.Room)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.Version, &m.ID, &m.Sender, &m.ClientID, &m.Text, &m.ReplyTo, &m.Created, &m.Edited, &m.Revoked, &m.Delivered, &m.Read, &m.Starred, &m.Pinned, &m.ReplyText); err != nil {
			return result, err
		}
		result.Messages = append(result.Messages, m)
	}
	result.Conversation = conversationFor(result.Messages, id.Actor)
	return result, rows.Err()
}
