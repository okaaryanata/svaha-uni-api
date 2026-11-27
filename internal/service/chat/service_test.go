package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	domain "github.com/okaaryanata/svaha-uni-api/internal/domain/chat"
)

type fakeRepository struct {
	tx    Transaction
	calls int
	err   error
}

func (r *fakeRepository) WithinTransaction(_ context.Context, fn func(Transaction) error) error {
	r.calls++
	if r.err != nil {
		return r.err
	}
	return fn(r.tx)
}

type roomTx struct {
	Transaction
	count   int
	created bool
}

func (t *roomTx) CountRooms() (int, error)              { return t.count, nil }
func (t *roomTx) InsertRoom(_, _ string, _ int64) error { t.created = true; return nil }
func TestInvalidIdentityAndCommandNeverReachStorage(t *testing.T) {
	repo := &fakeRepository{}
	s := New(repo, time.Now)
	if _, e := s.Login(context.Background(), "room", "secret", "other"); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatal(e)
	}
	result, e := s.Execute(context.Background(), domain.Identity{}, domain.Command{})
	if e != nil || result.ErrorCode != "invalid" || repo.calls != 0 {
		t.Fatalf("invalid input reached storage: %+v %v", result, e)
	}
}
func TestRoomCapacityIsCheckedInsideTransaction(t *testing.T) {
	tx := &roomTx{count: 1000}
	repo := &fakeRepository{tx: tx}
	s := New(repo, time.Now)
	if _, _, e := s.Create(context.Background()); e == nil || tx.created || repo.calls != 1 {
		t.Fatal("capacity bypassed")
	}
	tx.count = 0
	if _, _, e := s.Create(context.Background()); e != nil || !tx.created {
		t.Fatal(e)
	}
}
func TestRepositoryFailurePropagatesWithoutDomainRejection(t *testing.T) {
	failure := errors.New("storage unavailable")
	s := New(&fakeRepository{err: failure}, time.Now)
	_, e := s.Execute(context.Background(), domain.Identity{Room: "room", Actor: "a"}, domain.Command{ID: "operation", Action: "send"})
	if !errors.Is(e, failure) {
		t.Fatal(e)
	}
}
