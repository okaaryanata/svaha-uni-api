package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

type Command struct {
	ExpectedVersion *int64 `json:"expected_version,omitempty"`
	Type            string `json:"type"`
	ID              string `json:"id"`
	Action          string `json:"action"`
	MessageID       string `json:"message_id"`
	ClientID        string `json:"client_message_id"`
	Text            string `json:"text"`
	ReplyTo         string `json:"reply_to"`
	Enabled         bool   `json:"enabled"`
}

// Apply retains the convenient domain interface used by internal callers.
func (s *Store) Apply(ctx context.Context, id Identity, c Command) (string, error) {
	result, err := s.Execute(ctx, id, c)
	if err != nil {
		return "", err
	}
	if result.ErrorCode != "" {
		return "", result.domainError()
	}
	return result.MessageID, nil
}

type CommandResult struct {
	MessageID    string `json:"message_id,omitempty"`
	Revision     int64  `json:"revision"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

func (r CommandResult) domainError() error {
	switch r.ErrorCode {
	case "invalid":
		return ErrInvalid
	case "forbidden":
		return ErrForbidden
	case "expired":
		return ErrExpired
	case "version_conflict":
		return ErrVersionConflict
	case "idempotency_conflict":
		return ErrConflict
	default:
		return errors.New(r.ErrorMessage)
	}
}
func commandFailure(err error) CommandResult {
	code := "rejected"
	switch {
	case errors.Is(err, ErrInvalid):
		code = "invalid"
	case errors.Is(err, ErrForbidden):
		code = "forbidden"
	case errors.Is(err, ErrExpired):
		code = "expired"
	case errors.Is(err, ErrVersionConflict):
		code = "version_conflict"
	case errors.Is(err, ErrConflict):
		code = "idempotency_conflict"
	}
	return CommandResult{ErrorCode: code, ErrorMessage: err.Error()}
}

// Execute atomically stores each mutation and its replayable outcome.
func (s *Store) Execute(ctx context.Context, id Identity, c Command) (CommandResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(c.ID) == 0 || len(c.ID) > 128 {
		return commandFailure(ErrInvalid), nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CommandResult{}, err
	}
	defer tx.Rollback()
	var active int64
	if err = tx.QueryRowContext(ctx, "SELECT last_active FROM rooms WHERE id=?", id.Room).Scan(&active); err == sql.ErrNoRows {
		return commandFailure(ErrExpired), nil
	} else if err != nil {
		return CommandResult{}, err
	}
	if active < s.now().Add(-retention).UnixMilli() {
		return commandFailure(ErrExpired), nil
	}
	fingerprintCommand := c
	fingerprintCommand.ID = ""
	fingerprintCommand.Type = ""
	payload, _ := json.Marshal(fingerprintCommand)
	fingerprint := hash(string(payload))
	var storedFingerprint, storedJSON string
	err = tx.QueryRowContext(ctx, "SELECT fingerprint,result_json FROM command_results WHERE room_id=? AND actor_id=? AND command_id=?", id.Room, id.Actor, c.ID).Scan(&storedFingerprint, &storedJSON)
	if err == nil {
		if storedFingerprint != fingerprint {
			return commandFailure(ErrConflict), nil
		}
		var result CommandResult
		err = json.Unmarshal([]byte(storedJSON), &result)
		return result, err
	}
	if err != sql.ErrNoRows {
		return CommandResult{}, err
	}
	if _, err = tx.ExecContext(ctx, "SAVEPOINT mutation"); err != nil {
		return CommandResult{}, err
	}
	messageID, actionErr := s.applyTx(ctx, tx, id, c)
	result := CommandResult{MessageID: messageID}
	if actionErr != nil {
		if _, err = tx.ExecContext(ctx, "ROLLBACK TO mutation"); err != nil {
			return CommandResult{}, err
		}
		// Infrastructure errors are retryable and must not be remembered as rejection.
		if !errors.Is(actionErr, ErrInvalid) && !errors.Is(actionErr, ErrForbidden) && !errors.Is(actionErr, ErrExpired) && !errors.Is(actionErr, ErrVersionConflict) && !errors.Is(actionErr, ErrConflict) && actionErr.Error() != "demo message limit reached; start a new demo" {
			return CommandResult{}, actionErr
		}
		result = commandFailure(actionErr)
	}
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM rooms WHERE id=?", id.Room).Scan(&result.Revision); err != nil {
		return CommandResult{}, err
	}
	encoded, _ := json.Marshal(result)
	if _, err = tx.ExecContext(ctx, "INSERT INTO command_results VALUES(?,?,?,?,?)", id.Room, id.Actor, c.ID, fingerprint, string(encoded)); err != nil {
		return CommandResult{}, err
	}
	return result, tx.Commit()
}
func (s *Store) applyTx(ctx context.Context, tx *sql.Tx, id Identity, c Command) (string, error) {
	var err error
	var active int64
	if err := tx.QueryRowContext(ctx, "SELECT last_active FROM rooms WHERE id=?", id.Room).Scan(&active); err != nil {
		return "", ErrExpired
	}
	if active < s.now().Add(-retention).UnixMilli() {
		return "", ErrExpired
	}
	messageID := c.MessageID
	if c.Action == "send" {
		if len(c.ClientID) == 0 || len(c.ClientID) > 128 || strings.TrimSpace(c.Text) == "" || utf8.RuneCountInString(c.Text) > 2000 || len(c.ReplyTo) > 128 {
			return "", ErrInvalid
		}
		var fingerprint string
		err = tx.QueryRowContext(ctx, "SELECT id,request_hash FROM messages WHERE room_id=? AND sender_id=? AND client_id=?", id.Room, id.Actor, c.ClientID).Scan(&messageID, &fingerprint)
		if err == nil {
			if fingerprint != hash(c.Text+"\x00"+c.ReplyTo) {
				return "", ErrConflict
			}
			return messageID, nil
		}
		if err != sql.ErrNoRows {
			return "", err
		}
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages WHERE room_id=?", id.Room).Scan(&count); err != nil {
			return "", err
		}
		if count >= 500 {
			return "", errors.New("demo message limit reached; start a new demo")
		}
		if c.ReplyTo != "" {
			var unavailable bool
			err = tx.QueryRowContext(ctx, "SELECT revoked OR EXISTS(SELECT 1 FROM preferences WHERE message_id=messages.id AND actor_id=? AND hidden=1) FROM messages WHERE id=? AND room_id=?", id.Actor, c.ReplyTo, id.Room).Scan(&unavailable)
			if err != nil || unavailable {
				return "", ErrInvalid
			}
		}
		messageID = randomID()
		_, err = tx.ExecContext(ctx, "INSERT INTO messages(id,room_id,sender_id,client_id,request_hash,text,reply_to,created_at) VALUES(?,?,?,?,?,?,?,?)", messageID, id.Room, id.Actor, c.ClientID, hash(c.Text+"\x00"+c.ReplyTo), c.Text, c.ReplyTo, s.now().UnixMilli())
	} else {
		var sender string
		var revoked bool
		var version int64
		err = tx.QueryRowContext(ctx, "SELECT sender_id,revoked,version FROM messages WHERE id=? AND room_id=?", messageID, id.Room).Scan(&sender, &revoked, &version)
		if err == sql.ErrNoRows {
			return "", ErrInvalid
		}
		if err != nil {
			return "", err
		}
		switch c.Action {
		case "edit":
			if c.ExpectedVersion != nil && *c.ExpectedVersion != version {
				return "", ErrVersionConflict
			}
			if sender != id.Actor || revoked {
				return "", ErrForbidden
			}
			if strings.TrimSpace(c.Text) == "" || utf8.RuneCountInString(c.Text) > 2000 {
				return "", ErrInvalid
			}
			_, err = tx.ExecContext(ctx, "UPDATE messages SET text=?,edited_at=?,version=version+1 WHERE id=?", c.Text, s.now().UnixMilli(), messageID)
		case "revoke":
			if sender != id.Actor {
				return "", ErrForbidden
			}
			_, err = tx.ExecContext(ctx, "UPDATE messages SET text='',revoked=1,version=version+1 WHERE id=?", messageID)
			if err == nil {
				_, err = tx.ExecContext(ctx, "DELETE FROM pins WHERE message_id=?", messageID)
			}
			if err == nil {
				_, err = tx.ExecContext(ctx, "UPDATE preferences SET starred=0 WHERE message_id=?", messageID)
			}
		case "delete", "star":
			if c.Action == "star" && revoked {
				return "", ErrForbidden
			}
			column := "starred"
			if c.Action == "delete" {
				column = "hidden"
			}
			_, err = tx.ExecContext(ctx, "INSERT INTO preferences(room_id,actor_id,message_id,"+column+") VALUES(?,?,?,?) ON CONFLICT(room_id,actor_id,message_id) DO UPDATE SET "+column+"=excluded."+column, id.Room, id.Actor, messageID, c.Action == "delete" || c.Enabled)
			if err == nil && c.Action == "delete" {
				_, err = tx.ExecContext(ctx, "UPDATE preferences SET starred=0 WHERE actor_id=? AND message_id=?", id.Actor, messageID)
			}
		case "pin":
			if revoked {
				return "", ErrForbidden
			}
			if c.Enabled {
				_, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO pins VALUES(?,?)", id.Room, messageID)
			} else {
				_, err = tx.ExecContext(ctx, "DELETE FROM pins WHERE room_id=? AND message_id=?", id.Room, messageID)
			}
		case "delivered", "read":
			if sender == id.Actor {
				return "", ErrForbidden
			}
			if c.Action == "read" {
				_, err = tx.ExecContext(ctx, "UPDATE messages SET delivered=1,read=1 WHERE room_id=? AND sender_id!=? AND rowid <= (SELECT rowid FROM messages WHERE id=?)", id.Room, id.Actor, messageID)
			} else {
				_, err = tx.ExecContext(ctx, "UPDATE messages SET delivered=1 WHERE room_id=? AND sender_id!=? AND rowid <= (SELECT rowid FROM messages WHERE id=?)", id.Room, id.Actor, messageID)
			}
		default:
			return "", ErrInvalid
		}
	}
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, "UPDATE rooms SET revision=revision+1,last_active=? WHERE id=?", s.now().UnixMilli(), id.Room)
	if err != nil {
		return "", err
	}
	// Active sessions follow room activity; no heartbeat-only retention extension.
	_, err = tx.ExecContext(ctx, "UPDATE sessions SET expires=? WHERE room_id=?", s.now().Add(retention).UnixMilli(), id.Room)
	if err != nil {
		return "", err
	}
	return messageID, nil
}
