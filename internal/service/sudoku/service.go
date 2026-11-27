package sudoku

import (
	"context"
	"time"

	domain "github.com/okaaryanata/svaha-uni-api/internal/domain/sudoku"
)

type Service struct{}

func New() *Service { return &Service{} }
func (*Service) Solve(ctx context.Context, input [][]int) (domain.Board, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return domain.Solve(ctx, input)
}
