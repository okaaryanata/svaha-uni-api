package websocket

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"time"

	domain "github.com/okaaryanata/svaha-uni-api/internal/domain/chat"
	repo "github.com/okaaryanata/svaha-uni-api/internal/repository/sqlite"
	service "github.com/okaaryanata/svaha-uni-api/internal/service/chat"
)

type Identity = domain.Identity
type Message = domain.Message
type Snapshot = domain.Snapshot
type Command = domain.Command

var ErrForbidden = domain.ErrForbidden
var ErrInvalid = domain.ErrInvalid
var ErrExpired = domain.ErrExpired
var ErrUnauthorized = domain.ErrUnauthorized
var ErrConflict = domain.ErrConflict
var schema = func() string {
	b, e := os.ReadFile("../../repository/sqlite/migrations/001_init.sql")
	if e != nil {
		panic(e)
	}
	return string(b)
}()

type Store struct {
	*service.Service
	db   *sql.DB
	repo *repo.Store
	now  func() time.Time
}

func Open(path string) (*Store, error) {
	r, e := repo.Open(path)
	if e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		_ = r.Close()
		return nil, e
	}
	s := &Store{db: db, repo: r, now: time.Now}
	s.Service = service.New(r, func() time.Time { return s.now() })
	return s, nil
}
func (s *Store) Close() error { _ = s.db.Close(); return s.repo.Close() }
func randomID() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func hash(v string) string { b := sha256.Sum256([]byte(v)); return hex.EncodeToString(b[:]) }

var _ ChatService = (*Store)(nil)
