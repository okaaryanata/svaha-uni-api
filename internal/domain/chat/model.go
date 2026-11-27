package chat

import (
	"errors"
	"time"
)

const Retention = 24 * time.Hour

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

func ConversationFor(messages []Message, actor string) Conversation {
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

type CommandResult struct {
	MessageID    string `json:"message_id,omitempty"`
	Revision     int64  `json:"revision"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

func (r CommandResult) DomainError() error {
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
func CommandFailure(err error) CommandResult {
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

var ErrNotFound = errors.New("record not found")
var ErrMessageLimit = errors.New("demo message limit reached; start a new demo")

func Permanent(err error) bool {
	return errors.Is(err, ErrInvalid) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrExpired) || errors.Is(err, ErrVersionConflict) || errors.Is(err, ErrConflict) || errors.Is(err, ErrMessageLimit)
}
