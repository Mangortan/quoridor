package threat

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"quoridor/board"
	"quoridor/movegen"
)

// Позиции: случайные партии с повышенной долей стен (в них ловушки встречаются чаще).
func positions(n int, seed int64) []board.Board {
	rng := rand.New(rand.NewSource(seed))
	var out []board.Board
	for len(out) < n {
		b := board.New()
		for ply := 0; ply < 70 && len(out) < n && b.Winner() < 0; ply++ {
			if ply >= 8 && b.Walls[b.Turn] > 0 {
				out = append(out, b)
			}
			ms := movegen.Gen(&b, nil)
			// 60% ходов — стены (если есть), чтобы поле быстрее заполнялось
			var pick board.Move
			var walls []board.Move
			for _, m := range ms {
				if m.Kind() != board.KindPawn {
					walls = append(walls, m)
				}
			}
			if len(walls) > 0 && rng.Intn(100) < 60 {
				pick = walls[rng.Intn(len(walls))]
			} else {
				pick = ms[rng.Intn(len(ms))]
			}
			b = b.Apply(pick)
		}
	}
	return out
}

func TestFindSoundness(t *testing.T) { // найденный ход всегда легален
	for i, b := range positions(300, 1) {
		r := Find(&b, 2, 2)
		if r.Found {
			if !movegen.LegalWall(&b, r.Move.Kind() == board.KindH, r.Move.Idx()/8, r.Move.Idx()%8) {
				t.Fatalf("pos %d: illegal trap move", i)
			}
		}
	}
}

func TestDepth1MatchesBruteForce(t *testing.T) { // depth 1 — это просто «стена с gain >= thr»
	for i, b := range positions(300, 2) {
		a, e := Find(&b, 1, 2), FindExhaustive(&b, 1, 2)
		if a.Found != e.Found {
			t.Fatalf("pos %d: pruned=%v exhaustive=%v\n%+v", i, a.Found, e.Found, b)
		}
	}
}

// Сводка по ловушкам глубины 2: сколько позиций с настоящей двухстеночной ловушкой
// (одна стена не достаёт thr), и какую часть находит быстрый Find.
func TestTrapRecall(t *testing.T) {
	n := 1500
	if testing.Short() {
		n = 300
	}
	pos := positions(n, 3)
	trap, found, one, fast, slow := 0, 0, 0, time.Duration(0), time.Duration(0)
	var regress []board.Board
	for _, b := range pos {
		if FindExhaustive(&b, 1, 2).Found {
			one++
			continue
		}
		t0 := time.Now()
		e := FindExhaustive(&b, 2, 2)
		slow += time.Since(t0)
		if !e.Found {
			continue
		}
		trap++
		t0 = time.Now()
		a := Find(&b, 2, 2)
		fast += time.Since(t0)
		if a.Found {
			found++
		} else if len(regress) < 5 {
			regress = append(regress, b)
		}
	}
	t.Logf("positions %d: single-wall wins %d, true 2-wall traps %d, found by Find %d", len(pos), one, trap, found)
	if trap > 0 {
		t.Logf("recall %.0f%%; time per trap-position: exhaustive %v, Find %v", 100*float64(found)/float64(trap), slow/time.Duration(trap), fast/time.Duration(trap))
		if float64(found) < 0.9*float64(trap) {
			for _, b := range regress {
				fmt.Printf("missed: %+v\n", b)
			}
			t.Errorf("recall too low: %d/%d", found, trap)
		}
	}
}

