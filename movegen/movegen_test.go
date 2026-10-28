package movegen

import (
	"math/rand"
	"testing"

	"quoridor/board"
	"quoridor/path"
)

// ---- позиции ----

// Случайные легальные партии.
func randomPositions(n int, seed int64) []board.Board {
	rng := rand.New(rand.NewSource(seed))
	var out []board.Board
	for len(out) < n {
		b := board.New()
		for ply := 0; ply < 100 && len(out) < n && b.Winner() < 0; ply++ {
			out = append(out, b)
			if rng.Intn(100) < 45 && b.Walls[b.Turn] > 0 {
				moved := false
				for try := 0; try < 30 && !moved; try++ {
					h := rng.Intn(2) == 0
					r, c := rng.Intn(8), rng.Intn(8)
					if _, ok := b.WithWall(h, r, c); ok {
						b = b.Apply(board.WallMove(h, r, c))
						moved = true
					}
				}
				if moved {
					continue
				}
			}
			ms := b.PawnMoves(int(b.Turn), make([]uint8, 0, 8))
			b = b.Apply(board.PawnMove(int(ms[rng.Intn(len(ms))])))
		}
	}
	return out
}

// score1: разность путей после хода с точки зрения походившего (больше = лучше).
func score1(b board.Board, m board.Move) int {
	me := int(b.Turn)
	nb := b.Apply(m)
	if w := nb.Winner(); w >= 0 {
		if w == me {
			return 100
		}
		return -100
	}
	return path.Len(&nb, 1-me) - path.Len(&nb, me)
}

// Партии «жадных» ботов (1 шаг по score1 из отсечённого генератора, 15% случайных ходов):
// позиции ближе к реальной игре, чем чисто случайные.
func greedyPositions(n int, seed int64) []board.Board {
	rng := rand.New(rand.NewSource(seed))
	var out []board.Board
	for len(out) < n {
		b := board.New()
		for ply := 0; ply < 120 && len(out) < n && b.Winner() < 0; ply++ {
			out = append(out, b)
			ms := Gen(&b, nil)
			var m board.Move
			if rng.Intn(100) < 15 {
				m = ms[rng.Intn(len(ms))]
			} else {
				best, bs := ms[0], -1000
				for _, x := range ms {
					s := score1(b, x)*4 + rng.Intn(4) // случайный разбор ничьих
					if s > bs {
						best, bs = x, s
					}
				}
				m = best
			}
			b = b.Apply(m)
		}
	}
	return out
}

func inSet(ms []board.Move, m board.Move) bool {
	for _, x := range ms {
		if x == m {
			return true
		}
	}
	return false
}

// ---- тесты ----

// Правило контактов не должно менять легальность ни одной стены.
func TestLegalWallMatchesWithWall(t *testing.T) {
	ps := append(randomPositions(1500, 1), greedyPositions(1500, 2)...)
	skipped, total := 0, 0
	for i, b := range ps {
		o := occupied(&b)
		for _, h := range []bool{true, false} {
			for r := 0; r < 8; r++ {
				for c := 0; c < 8; c++ {
					_, want := b.WithWall(h, r, c)
					if got := LegalWall(&b, h, r, c); got != want {
						t.Fatalf("pos %d wall h=%v (%d,%d): got %v want %v\n%+v", i, h, r, c, got, want, b)
					}
					if placeable(&b, h, r, c) {
						total++
						if contacts(&o, h, r, c) < 2 {
							skipped++
						}
					}
				}
			}
		}
	}
	t.Logf("BFS skipped for %d of %d placeable walls (%.0f%%)", skipped, total, 100*float64(skipped)/float64(total))
}

func TestGenSubsetOfFullAndHasPawnMoves(t *testing.T) {
	for i, b := range append(randomPositions(500, 3), greedyPositions(500, 4)...) {
		full := b.Moves(nil)
		gen := Gen(&b, nil)
		for _, m := range gen {
			if !inSet(full, m) {
				t.Fatalf("pos %d: Gen produced illegal move %v\n%+v", i, m, b)
			}
		}
		for _, m := range full {
			if m.Kind() == board.KindPawn && !inSet(gen, m) {
				t.Fatalf("pos %d: pawn move %v missing", i, m)
			}
		}
	}
}

func TestBranching(t *testing.T) {
	for name, ps := range map[string][]board.Board{
		"random": randomPositions(3000, 5),
		"greedy": greedyPositions(3000, 6),
	} {
		sum, sumFull, max := 0, 0, 0
		for _, b := range ps {
			n := len(Gen(&b, nil))
			sum += n
			sumFull += len(b.Moves(nil))
			if n > max {
				max = n
			}
		}
		avg := float64(sum) / float64(len(ps))
		t.Logf("%s: avg branching %.1f (full %.1f), max %d", name, avg, float64(sumFull)/float64(len(ps)), max)
		if avg > 25 {
			t.Errorf("%s: average branching %.1f > 25", name, avg)
		}
	}
}

// value2: минимакс на 2 хода (мой ход, ответ соперника) по разности путей.
func value2(b board.Board, gen func(*board.Board, []board.Move) []board.Move) int {
	me := int(b.Turn)
	best := -1000
	for _, m := range gen(&b, nil) {
		nb := b.Apply(m)
		if nb.Winner() >= 0 {
			if nb.Winner() == me {
				return 100
			}
			continue
		}
		worst := 1000
		for _, r := range gen(&nb, nil) {
			nn := nb.Apply(r)
			var v int
			if w := nn.Winner(); w >= 0 {
				v = -100 // раз ответ соперника привёл к победе, это его победа
			} else {
				v = path.Len(&nn, 1-me) - path.Len(&nn, me)
			}
			if v < worst {
				worst = v
			}
		}
		if worst > best {
			best = worst
		}
	}
	return best
}

func bestValue1(b board.Board, ms []board.Move) int {
	best := -1000
	for _, m := range ms {
		if s := score1(b, m); s > best {
			best = s
		}
	}
	return best
}

// Лучшая достижимая оценка (1 и 2 хода) полным и отсечённым генератором должна совпадать.
func TestSameBestValue(t *testing.T) {
	n := 300
	if testing.Short() {
		n = 40
	}
	ps := append(randomPositions(n/2, 7), greedyPositions(n/2, 8)...)
	same1, same2 := 0, 0
	for _, b := range ps {
		if bestValue1(b, b.Moves(nil)) == bestValue1(b, Gen(&b, nil)) {
			same1++
		}
		full := func(x *board.Board, out []board.Move) []board.Move { return x.Moves(out) }
		if value2(b, full) == value2(b, Gen) {
			same2++
		}
	}
	t.Logf("same best value: 1-ply %d/%d, 2-ply %d/%d", same1, len(ps), same2, len(ps))
	if float64(same1) < 0.95*float64(len(ps)) || float64(same2) < 0.85*float64(len(ps)) {
		t.Errorf("pruning loses too much")
	}
}

func BenchmarkGen(bm *testing.B) {
	ps := greedyPositions(500, 9)
	buf := make([]board.Move, 0, 160)
	bm.ResetTimer()
	for i := 0; i < bm.N; i++ {
		buf = Gen(&ps[i%len(ps)], buf[:0])
	}
}

func BenchmarkFullMoves(bm *testing.B) {
	ps := greedyPositions(500, 9)
	buf := make([]board.Move, 0, 160)
	bm.ResetTimer()
	for i := 0; i < bm.N; i++ {
		buf = ps[i%len(ps)].Moves(buf[:0])
	}
}

