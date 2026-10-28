package search

import (
	"testing"

	"quoridor/board"
)

// Если позиция после хода m1 уже была в реальной партии, поиск считает её ничьей (0).
// При eval = -100 («мне плохо») любой другой ход даёт +100, поэтому m1 выбирать нельзя.
func TestHistoryRepetitionAvoided(t *testing.T) {
	b := board.New()
	s := New(12)
	s.Eval = func(board.Board) int { return -100 }
	m1 := board.PawnMove(13) // (0,4) -> (1,4)
	nb := b.Apply(m1)
	s.SetHistory([]uint64{Hash(&nb)})
	r := s.Search(b, Limits{MaxDepth: 1})
	if r.Move == m1 {
		t.Fatalf("поиск выбрал ход, ведущий к повтору позиции из истории")
	}
	if r.Score != 100 {
		t.Fatalf("score = %d, want 100", r.Score)
	}
}

// Без истории тот же ход не штрафуется (контроль: тест выше проверяет именно историю).
func TestNoHistoryNoPenalty(t *testing.T) {
	b := board.New()
	s := New(12)
	s.Eval = func(board.Board) int { return -100 }
	r := s.Search(b, Limits{MaxDepth: 1})
	if r.Score != 100 {
		t.Fatalf("score = %d, want 100", r.Score)
	}
}


