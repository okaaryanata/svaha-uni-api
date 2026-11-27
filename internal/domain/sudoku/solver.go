// Package sudoku adapts the row/column/box backtracking solver from
// github.com/okaaryanata/sudoku-solver, adding input validation and cancellation.
package sudoku

import (
	"context"
	"errors"
)

var ErrInvalid = errors.New("board must be 9 by 9, contain integers from 0 to 9, and have no duplicate clues")
var ErrUnsolvable = errors.New("board has no solution")

type Board [9][9]int

func Solve(ctx context.Context, input [][]int) (Board, error) {
	var board Board
	if len(input) != 9 {
		return board, ErrInvalid
	}
	for row := range 9 {
		if len(input[row]) != 9 {
			return Board{}, ErrInvalid
		}
		for col, value := range input[row] {
			if value < 0 || value > 9 {
				return Board{}, ErrInvalid
			}
			if value != 0 && !valid(board, row, col, value) {
				return Board{}, ErrInvalid
			}
			board[row][col] = value
		}
	}
	solved, err := search(ctx, &board)
	if err != nil {
		return Board{}, err
	}
	if !solved {
		return Board{}, ErrUnsolvable
	}
	return board, nil
}
func valid(board Board, row, col, value int) bool {
	for i := range 9 {
		if board[row][i] == value || board[i][col] == value {
			return false
		}
	}
	for r := row / 3 * 3; r < row/3*3+3; r++ {
		for c := col / 3 * 3; c < col/3*3+3; c++ {
			if board[r][c] == value {
				return false
			}
		}
	}
	return true
}
func search(ctx context.Context, board *Board) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// Pick the most constrained empty cell to keep sparse puzzles responsive.
	row, col := -1, -1
	var choices []int
	for r := range 9 {
		for c := range 9 {
			if board[r][c] != 0 {
				continue
			}
			candidates := make([]int, 0, 9)
			for n := 1; n <= 9; n++ {
				if valid(*board, r, c, n) {
					candidates = append(candidates, n)
				}
			}
			if len(candidates) == 0 {
				return false, nil
			}
			if row == -1 || len(candidates) < len(choices) {
				row, col, choices = r, c, candidates
			}
		}
	}
	if row == -1 {
		return true, nil
	}
	for _, value := range choices {
		board[row][col] = value
		ok, err := search(ctx, board)
		if ok || err != nil {
			return ok, err
		}
		board[row][col] = 0
	}
	return false, nil
}
