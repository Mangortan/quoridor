package path

import (
	"math/rand"
	"testing"

	"quoridor/board"
)

// ---- независимый эталон: рёбра проверяются геометрически по списку стен ----

type wall struct {
	h    bool
	r, c int
}

func decode(hw, vw uint64) (ws []wall) {
	for i := 0; i < 64; i++ {
		if hw>>uint(i)&1 != 0 {
			ws = append(ws, wall{true, i / 8, i % 8})
		}
		if vw>>uint(i)&1 != 0 {
			ws = append(ws, wall{false, i / 8, i % 8})
		}
	}
	return
}

func blocked(ws []wall, r1, c1, r2, c2 int) bool {
	for _, w := range ws {
		if r1 != r2 {
			lo := r1
			if r2 < lo {
				lo = r2
			}
			if w.h && w.r == lo && (c1 == w.c || c1 == w.c+1) {
				return true
			}
		} else {
			lo := c1
			if c2 < lo {
				lo = c2
			}
			if !w.h && w.c == lo && (r1 == w.r || r1 == w.r+1) {
				return true
			}
		}
	}
	return false
}

func refDist(hw, vw uint64, goal int) (d [81]int8) {
	ws := decode(hw, vw)
	for i := range d {
		d[i] = -1
	}
	var q []int
	for c := 0; c < 9; c++ {
		d[goal*9+c] = 0
		q = append(q, goal*9+c)
	}
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		r, c := cur/9, cur%9
		for _, dd := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nr, nc := r+dd[0], c+dd[1]
			if nr < 0 || nr > 8 || nc < 0 || nc > 8 || blocked(ws, r, c, nr, nc) {
				continue
			}
			if d[nr*9+nc] < 0 {
				d[nr*9+nc] = d[cur] + 1
				q = append(q, nr*9+nc)
			}
		}
	}
	return
}

// ---- генераторы позиций ----

// Позиции из случайных легальных партий (прыжки, стены, разные стадии).
func gamePositions(n int, seed int64) []board.Board {
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

func TestFieldVsReferenceOnGames(t *testing.T) {
	for i, b := range gamePositions(10000, 1) {
		for p := 0; p < 2; p++ {
			g := board.GoalRow(p)
			got, want := Field(&b, g), refDist(b.HW, b.VW, g)
			if got != want {
				t.Fatalf("pos %d goal %d: Field != ref\n%+v", i, g, b)
			}
			if old := b.Dist(g); old != got {
				t.Fatalf("pos %d goal %d: Field != board.Dist", i, g)
			}
			if l := Len(&b, p); l != int(want[b.P[p]]) {
				t.Fatalf("pos %d p%d: Len=%d want %d", i, p, l, want[b.P[p]])
			}
		}
	}
}

// Сырые случайные маски (стены могут пересекаться и отрезать части поля): проверяем -1.
func TestFieldVsReferenceRandomMasks(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	unreachable := 0
	for i := 0; i < 10000; i++ {
		var b board.Board
		for k := rng.Intn(40); k > 0; k-- {
			if rng.Intn(2) == 0 {
				b.HW |= 1 << uint(rng.Intn(64))
			} else {
				b.VW |= 1 << uint(rng.Intn(64))
			}
		}
		b.P = [2]uint8{uint8(rng.Intn(81)), uint8(rng.Intn(81))}
		for p := 0; p < 2; p++ {
			g := board.GoalRow(p)
			got, want := Field(&b, g), refDist(b.HW, b.VW, g)
			if got != want {
				t.Fatalf("mask %d goal %d: Field != ref (HW=%#x VW=%#x)", i, g, b.HW, b.VW)
			}
			if Len(&b, p) != int(want[b.P[p]]) {
				t.Fatalf("mask %d p%d: Len mismatch", i, p)
			}
			if want[b.P[p]] < 0 {
				unreachable++
			}
		}
	}
	t.Logf("unreachable pawn cases covered: %d", unreachable)
}

func TestStart(t *testing.T) {
	b := board.New()
	if Len(&b, 0) != 8 || Len(&b, 1) != 8 {
		t.Fatal("start Len must be 8")
	}
}

// ---- бенчмарки ----

var sink int8
var sinkI int

func BenchmarkFieldStart(bm *testing.B) {
	b := board.New()
	for i := 0; i < bm.N; i++ {
		d := Field(&b, 8)
		sink = d[4]
	}
}

func BenchmarkFieldMid(bm *testing.B) {
	ps := gamePositions(1000, 3)
	bm.ResetTimer()
	for i := 0; i < bm.N; i++ {
		d := Field(&ps[i%len(ps)], 8)
		sink = d[4]
	}
}

func BenchmarkLenMid(bm *testing.B) {
	ps := gamePositions(1000, 3)
	bm.ResetTimer()
	for i := 0; i < bm.N; i++ {
		sinkI = Len(&ps[i%len(ps)], 0)
	}
}

func BenchmarkBoardDistMid(bm *testing.B) { // старая реализация для сравнения
	ps := gamePositions(1000, 3)
	bm.ResetTimer()
	for i := 0; i < bm.N; i++ {
		d := ps[i%len(ps)].Dist(8)
		sink = d[4]
	}
}

