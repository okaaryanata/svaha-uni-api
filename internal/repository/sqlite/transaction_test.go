package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	repo "github.com/okaaryanata/svaha-uni-api/internal/repository/sqlite"
	service "github.com/okaaryanata/svaha-uni-api/internal/service/chat"
)

func TestTransactionRollsBackCallbackFailure(t *testing.T) {
	store, e := repo.Open(filepath.Join(t.TempDir(), "chat.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = store.Close() }()
	failure := errors.New("failed after insert")
	e = store.WithinTransaction(context.Background(), func(tx service.Transaction) error {
		if e := tx.InsertRoom("rollback", "hash", 1); e != nil {
			return e
		}
		return failure
	})
	if !errors.Is(e, failure) {
		t.Fatal(e)
	}
	e = store.WithinTransaction(context.Background(), func(tx service.Transaction) error {
		count, e := tx.CountRooms()
		if count != 0 {
			t.Fatal("partial transaction committed")
		}
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
}
