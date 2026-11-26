package chat

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"
)

type Command struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Action    string `json:"action"`
	MessageID string `json:"message_id"`
	ClientID  string `json:"client_message_id"`
	Text      string `json:"text"`
	ReplyTo   string `json:"reply_to"`
	Enabled   bool   `json:"enabled"`
}

// Apply serializes mutations and commits before the hub publishes snapshots.
func (s *Store) Apply(ctx context.Context, id Identity, c Command) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(c.ID) == 0 || len(c.ID) > 128 {
		return "", ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
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
		err = tx.QueryRowContext(ctx, "SELECT sender_id,revoked FROM messages WHERE id=? AND room_id=?", messageID, id.Room).Scan(&sender, &revoked)
		if err == sql.ErrNoRows {
			return "", ErrInvalid
		}
		if err != nil {
			return "", err
		}
		switch c.Action {
		case "edit":
			if sender != id.Actor || revoked {
				return "", ErrForbidden
			}
			if strings.TrimSpace(c.Text) == "" || utf8.RuneCountInString(c.Text) > 2000 {
				return "", ErrInvalid
			}
			_, err = tx.ExecContext(ctx, "UPDATE messages SET text=?,edited_at=? WHERE id=?", c.Text, s.now().UnixMilli(), messageID)
		case "revoke":
			if sender != id.Actor {
				return "", ErrForbidden
			}
			_, err = tx.ExecContext(ctx, "UPDATE messages SET text='',revoked=1 WHERE id=?", messageID)
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
	return messageID, tx.Commit()
}
