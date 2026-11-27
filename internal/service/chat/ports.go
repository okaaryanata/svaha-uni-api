package chat

import (
	"context"

	domain "github.com/okaaryanata/svaha-uni-api/internal/domain/chat"
)

// Repository controls the lifetime of a transaction, never application policy.
type Repository interface {
	WithinTransaction(context.Context, func(Transaction) error) error
}

// Transaction exposes only the persistence operations needed by chat use cases.
// Implementations must roll back all writes when the callback returns an error.
type Transaction interface {
	CountRooms() (int, error)
	InsertRoom(string, string, int64) error
	Room(string, string) (int64, int64, error)
	Session(string) (domain.Identity, int64, int64, error)
	InsertSession(string, string, string, int64) error
	TrimSessions(string, string) error
	Touch(string, int64, int64, bool) error
	Prune(int64, int64) error
	Messages(domain.Identity) ([]domain.Message, error)
	Outcome(domain.Identity, string) (string, domain.CommandResult, error)
	SaveOutcome(domain.Identity, string, string, domain.CommandResult) error
	Savepoint() error
	RollbackMutation() error
	Sent(domain.Identity, string) (string, string, error)
	CountMessages(string) (int, error)
	ReplyAvailable(domain.Identity, string) (bool, error)
	Message(domain.Identity, string) (domain.Message, error)
	InsertMessage(domain.Identity, domain.Command, string, string, int64) error
	Edit(string, string, int64) error
	Revoke(string) error
	Preference(domain.Identity, string, bool, bool) error
	Pin(string, string, bool) error
	Receipt(domain.Identity, string, bool) error
}
