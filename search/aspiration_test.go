package search

import (
	"testing"

	"quoridor/board"
)

// Узкие окна и прыжок по глубине не должны менять оценку на заданной глубине.
func TestAspirationAndReuseKeepScore(t *testing.T) {
	b := board.New()
	warm := New(16) // один поисковик на всю серию: TT и lastDepth переходят между ходами
	for i := 0; i < 4; i++ {
		ref := New(16)
		ref.NoAspiration = true
		want := ref.Search(b, Limits{MaxDepth: 4})
		got := warm.Search(b, Limits{MaxDepth: 4})
		if got.Score != want.Score {
			t.Fatalf("ход %d: score %d, ожидали %d", i, got.Score, want.Score)
		}
		b = b.Apply(want.Move)
	}
}

