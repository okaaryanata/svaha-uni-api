// Package chat implements chat use cases independently of SQL and network transports.
package chat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	domain "github.com/okaaryanata/svaha-uni-api/internal/domain/chat"
)

type Service struct {
	repo Repository
	now  func() time.Time
}

func New(repo Repository, now func() time.Time) *Service { return &Service{repo: repo, now: now} }
func randomID() (string, error) {
	var b [32]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}
func hash(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func (s *Service) Create(ctx context.Context) (room, secret string, err error) {
	room, err = randomID()
	if err != nil {
		return
	}
	secret, err = randomID()
	if err != nil {
		return
	}
	err = s.repo.WithinTransaction(ctx, func(tx Transaction) error {
		count, e := tx.CountRooms()
		if e != nil {
			return e
		}
		if count >= 1000 {
			return errors.New("demo room capacity reached; retry later")
		}
		return tx.InsertRoom(room, hash(secret), s.now().UnixMilli())
	})
	return
}
func (s *Service) Login(ctx context.Context, room, secret, actor string) (string, error) {
	if actor != "a" && actor != "b" {
		return "", domain.ErrUnauthorized
	}
	token, err := randomID()
	if err != nil {
		return "", err
	}
	err = s.repo.WithinTransaction(ctx, func(tx Transaction) error {
		_, active, e := tx.Room(room, hash(secret))
		if errors.Is(e, domain.ErrNotFound) {
			return domain.ErrUnauthorized
		}
		if e != nil {
			return e
		}
		now := s.now()
		if active < now.Add(-domain.Retention).UnixMilli() {
			return domain.ErrExpired
		}
		if e = tx.InsertSession(hash(token), room, actor, now.Add(domain.Retention).UnixMilli()); e != nil {
			return e
		}
		if e = tx.TrimSessions(room, actor); e != nil {
			return e
		}
		return tx.Touch(room, now.UnixMilli(), 0, false)
	})
	return token, err
}
func (s *Service) Authenticate(ctx context.Context, token string) (id domain.Identity, err error) {
	err = s.repo.WithinTransaction(ctx, func(tx Transaction) error {
		var expires, active int64
		var e error
		id, expires, active, e = tx.Session(hash(token))
		if errors.Is(e, domain.ErrNotFound) {
			return domain.ErrUnauthorized
		}
		if e != nil {
			return e
		}
		now := s.now()
		if expires < now.UnixMilli() || active < now.Add(-domain.Retention).UnixMilli() {
			return domain.ErrExpired
		}
		return nil
	})
	return
}
func (s *Service) Prune(ctx context.Context) error {
	return s.repo.WithinTransaction(ctx, func(tx Transaction) error {
		now := s.now()
		return tx.Prune(now.Add(-domain.Retention).UnixMilli(), now.UnixMilli())
	})
}
func (s *Service) Snapshot(ctx context.Context, id domain.Identity) (state domain.Snapshot, err error) {
	state.Type = "snapshot"
	state.Messages = []domain.Message{}
	err = s.repo.WithinTransaction(ctx, func(tx Transaction) error {
		revision, active, e := tx.Room(id.Room, "")
		if errors.Is(e, domain.ErrNotFound) {
			return domain.ErrExpired
		}
		if e != nil {
			return e
		}
		if active < s.now().Add(-domain.Retention).UnixMilli() {
			return domain.ErrExpired
		}
		state.Revision = revision
		state.Messages, e = tx.Messages(id)
		return e
	})
	state.Conversation = domain.ConversationFor(state.Messages, id.Actor)
	return
}
func (s *Service) Apply(ctx context.Context, id domain.Identity, c domain.Command) (string, error) {
	r, e := s.Execute(ctx, id, c)
	if e != nil {
		return "", e
	}
	if r.ErrorCode != "" {
		return "", r.DomainError()
	}
	return r.MessageID, nil
}

// Execute commits the mutation and its replayable outcome in the same transaction.
func (s *Service) Execute(ctx context.Context, id domain.Identity, c domain.Command) (result domain.CommandResult, err error) {
	if len(c.ID) == 0 || len(c.ID) > 128 {
		return domain.CommandFailure(domain.ErrInvalid), nil
	}
	err = s.repo.WithinTransaction(ctx, func(tx Transaction) error {
		_, active, e := tx.Room(id.Room, "")
		if errors.Is(e, domain.ErrNotFound) {
			result = domain.CommandFailure(domain.ErrExpired)
			return nil
		}
		if e != nil {
			return e
		}
		if active < s.now().Add(-domain.Retention).UnixMilli() {
			result = domain.CommandFailure(domain.ErrExpired)
			return nil
		}
		fingerprintCommand := c
		fingerprintCommand.ID = ""
		fingerprintCommand.Type = ""
		payload, e := json.Marshal(fingerprintCommand)
		if e != nil {
			return e
		}
		fingerprint := hash(string(payload))
		stored, cached, e := tx.Outcome(id, c.ID)
		if e == nil {
			if stored != fingerprint {
				result = domain.CommandFailure(domain.ErrConflict)
			} else {
				result = cached
			}
			return nil
		}
		if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		if e = tx.Savepoint(); e != nil {
			return e
		}
		messageID, actionErr := s.apply(tx, id, c)
		result = domain.CommandResult{MessageID: messageID}
		if actionErr != nil {
			if e = tx.RollbackMutation(); e != nil {
				return e
			}
			if !domain.Permanent(actionErr) {
				return actionErr
			}
			result = domain.CommandFailure(actionErr)
		}
		result.Revision, _, e = tx.Room(id.Room, "")
		if e != nil {
			return e
		}
		return tx.SaveOutcome(id, c.ID, fingerprint, result)
	})
	return
}
func (s *Service) apply(tx Transaction, id domain.Identity, c domain.Command) (string, error) {
	messageID := c.MessageID
	now := s.now()
	var err error
	if c.Action == "send" {
		if len(c.ClientID) == 0 || len(c.ClientID) > 128 || strings.TrimSpace(c.Text) == "" || utf8.RuneCountInString(c.Text) > 2000 || len(c.ReplyTo) > 128 {
			return "", domain.ErrInvalid
		}
		sent, fingerprint, e := tx.Sent(id, c.ClientID)
		if e == nil {
			if fingerprint != hash(c.Text+"\x00"+c.ReplyTo) {
				return "", domain.ErrConflict
			}
			return sent, nil
		}
		if !errors.Is(e, domain.ErrNotFound) {
			return "", e
		}
		count, e := tx.CountMessages(id.Room)
		if e != nil {
			return "", e
		}
		if count >= 500 {
			return "", domain.ErrMessageLimit
		}
		if c.ReplyTo != "" {
			available, e := tx.ReplyAvailable(id, c.ReplyTo)
			if errors.Is(e, domain.ErrNotFound) || !available && e == nil {
				return "", domain.ErrInvalid
			}
			if e != nil {
				return "", e
			}
		}
		messageID, err = randomID()
		if err != nil {
			return "", err
		}
		err = tx.InsertMessage(id, c, messageID, hash(c.Text+"\x00"+c.ReplyTo), now.UnixMilli())
	} else {
		message, e := tx.Message(id, messageID)
		if errors.Is(e, domain.ErrNotFound) {
			return "", domain.ErrInvalid
		}
		if e != nil {
			return "", e
		}
		switch c.Action {
		case "edit":
			if c.ExpectedVersion != nil && *c.ExpectedVersion != message.Version {
				return "", domain.ErrVersionConflict
			}
			if message.Sender != id.Actor || message.Revoked {
				return "", domain.ErrForbidden
			}
			if strings.TrimSpace(c.Text) == "" || utf8.RuneCountInString(c.Text) > 2000 {
				return "", domain.ErrInvalid
			}
			err = tx.Edit(messageID, c.Text, now.UnixMilli())
		case "revoke":
			if message.Sender != id.Actor {
				return "", domain.ErrForbidden
			}
			err = tx.Revoke(messageID)
		case "delete", "star":
			if c.Action == "star" && message.Revoked {
				return "", domain.ErrForbidden
			}
			err = tx.Preference(id, messageID, c.Action == "delete", c.Enabled)
		case "pin":
			if message.Revoked {
				return "", domain.ErrForbidden
			}
			err = tx.Pin(id.Room, messageID, c.Enabled)
		case "read", "delivered":
			if message.Sender == id.Actor {
				return "", domain.ErrInvalid
			}
			err = tx.Receipt(id, messageID, c.Action == "read")
		default:
			return "", domain.ErrInvalid
		}
	}
	if err != nil {
		return "", err
	}
	if err = tx.Touch(id.Room, now.UnixMilli(), now.Add(domain.Retention).UnixMilli(), true); err != nil {
		return "", err
	}
	return messageID, nil
}
