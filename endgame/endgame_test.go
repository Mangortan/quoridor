package endgame_test

import (
	"math/rand"
	"testing"

	"quoridor/board"
	"quoridor/endgame"
	"quoridor/match"
	"quoridor/path"
	"quoridor/search"
)

// randomPosition: случайная партия со стенами, затем стены «закончились» (стены на доске остаются),
// а пешки переставлены ближе к целям, чтобы исход был виден неглубоким поиском.
func randomPosition(rng *rand.Rand) board.Board {
	for {
		b := board.New()
		for _, m := range match.RandomOpening(rng, 10+rng.Intn(50)) {
			b = b.Apply(m)
		}
		b.Walls = [2]uint8{0, 0}
		b.P[0] = uint8((4+rng.Intn(4))*9 + rng.Intn(9)) // строки 4..7
		b.P[1] = uint8((1+rng.Intn(4))*9 + rng.Intn(9)) // строки 1..4
		b.Turn = uint8(rng.Intn(2))
		if b.P[0] == b.P[1] || b.Winner() >= 0 || path.Len(&b, 0) < 0 || path.Len(&b, 1) < 0 {
			continue
		}
		return b
	}
}

// Решатель совпадает с независимым полным поиском (без эндшпильной подмены) на коротких выигрышах и проигрышах.
func TestSolverAgreesWithSearch(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	s := search.New(18)
	s.NoEndgame = true
	s.Eval = func(board.Board) int { return 0 }
	const depth = 7
	checked, decisive := 0, 0
	for i := 0; i < 400; i++ {
		b := randomPosition(rng)
		tb := endgame.Solve(&b)
		res, plies := tb.Outcome(&b)
		r := s.Search(b, search.Limits{MaxDepth: depth})
		want, wantPlies := 0, 0
		if r.Score > search.MateThr {
			want, wantPlies = 1, search.Mate-r.Score
		} else if r.Score < -search.MateThr {
			want, wantPlies = -1, search.Mate+r.Score
		}
		checked++
		if want != 0 {
			decisive++
			if res != want || plies != wantPlies {
				t.Fatalf("position %s: solver=(%d,%d) search=(%d,%d)", match.FormatBoard(b), res, plies, want, wantPlies)
			}
		} else if res != 0 && plies <= depth {
			t.Fatalf("position %s: solver says decisive in %d, search sees none within %d", match.FormatBoard(b), plies, depth)
		}
	}
	t.Logf("проверено %d позиций, из них с исходом внутри глубины %d: %d", checked, depth, decisive)
	if decisive < 20 {
		t.Fatalf("слишком мало решающих позиций для проверки: %d", decisive)
	}
}

// Партия, где оба играют Best, заканчивается ровно за предсказанное число полуходов, а у ничьей — не проигрывает.
func TestBestPlayoutMatchesPrediction(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	n := 0
	for i := 0; i < 300; i++ {
		b := randomPosition(rng)
		tb := endgame.Solve(&b)
		res, plies := tb.Outcome(&b)
		if res == 0 {
			continue
		}
		n++
		start := b
		for k := 0; k < plies; k++ {
			if b.Winner() >= 0 {
				t.Fatalf("игра закончилась раньше: %d из %d", k, plies)
			}
			m, _, _ := tb.Best(&b, nil)
			b = b.Apply(m)
		}
		want := int(start.Turn)
		if res < 0 {
			want ^= 1
		}
		if b.Winner() != want {
			t.Fatalf("после %d полуходов победитель %d, ожидали %d; старт %s", plies, b.Winner(), want, match.FormatBoard(start))
		}
	}
	if n < 50 {
		t.Fatalf("мало решающих позиций: %d", n)
	}
}

// Простейший выигрыш: синяя пешка в одном шаге от цели и ходит сейчас.
func TestWinInOne(t *testing.T) {
	b := board.New()
	b.Walls = [2]uint8{0, 0}
	b.P = [2]uint8{7 * 9, 80}
	tb := endgame.Solve(&b)
	if res, plies := tb.Outcome(&b); res != 1 || plies != 1 {
		t.Fatalf("res=%d plies=%d, want win in 1", res, plies)
	}
	m, res, plies := tb.Best(&b, nil)
	if res != 1 || plies != 1 || m != board.PawnMove(8*9) {
		t.Fatalf("best = %s res=%d plies=%d", match.FormatMove(m), res, plies)
	}
}

