package sudoku

import (
	"context"
	"errors"
	"testing"
)

func sample() [][]int {
	return [][]int{
		{5, 3, 0, 0, 7, 0, 0, 0, 0}, {6, 0, 0, 1, 9, 5, 0, 0, 0}, {0, 9, 8, 0, 0, 0, 0, 6, 0},
		{8, 0, 0, 0, 6, 0, 0, 0, 3}, {4, 0, 0, 8, 0, 3, 0, 0, 1}, {7, 0, 0, 0, 2, 0, 0, 0, 6},
		{0, 6, 0, 0, 0, 0, 2, 8, 0}, {0, 0, 0, 4, 1, 9, 0, 0, 5}, {0, 0, 0, 0, 8, 0, 0, 7, 9}}
}
func TestSolve(t *testing.T) {
	input := sample()
	board, err := Solve(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for r := range 9 {
		for c := range 9 {
			value := board[r][c]
			if value < 1 || value > 9 {
				t.Fatal("empty result")
			}
			if input[r][c] != 0 && input[r][c] != value {
				t.Fatal("changed clue")
			}
			copy := board
			copy[r][c] = 0
			if !valid(copy, r, c, value) {
				t.Fatal("invalid solution")
			}
		}
	}
	if input[0][2] != 0 {
		t.Fatal("mutated input")
	}
}
func TestOriginalRepositoryPuzzle(t *testing.T) {
	_, err := Solve(context.Background(), [][]int{{0, 2, 0, 0, 0, 0, 0, 0, 0}, {0, 0, 1, 0, 0, 9, 7, 0, 0}, {4, 0, 3, 0, 0, 0, 8, 0, 0}, {3, 0, 2, 0, 0, 5, 0, 0, 0}, {0, 0, 0, 0, 0, 7, 9, 1, 0}, {0, 0, 6, 0, 0, 0, 0, 0, 4}, {6, 8, 0, 0, 0, 0, 4, 0, 0}, {0, 0, 0, 7, 0, 0, 0, 3, 0}, {0, 0, 0, 5, 0, 0, 0, 0, 2}})
	if err != nil {
		t.Fatal(err)
	}
}
func TestInvalid(t *testing.T) {
	for _, kind := range []string{"shape", "range", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			input := sample()
			switch kind {
			case "shape":
				input[0] = []int{1}
			case "range":
				input[0][2] = 10
			case "duplicate":
				input[0][2] = 5
			}
			if _, err := Solve(context.Background(), input); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v", err)
			}
		})
	}
}
func TestUnsolvable(t *testing.T) {
	input := sample()
	input[0][2] = 1
	if _, err := Solve(context.Background(), input); !errors.Is(err, ErrUnsolvable) {
		t.Fatalf("got %v", err)
	}
}
func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Solve(ctx, sample()); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
