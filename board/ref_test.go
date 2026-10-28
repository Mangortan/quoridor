package board

// Независимая медленная эталонная реализация: другая модель данных
// (список стен + проверка рёбер геометрически + DFS), ничего не берёт из board.go
// кроме кодирования Move.

import (
	"math/rand"
	"sort"
	"testing"
)

type refWall struct {
	h    bool
	r, c int
}

type refPos struct {
	pawn  [2][2]int // [игрок]{row,col}
	walls [2]int
	turn  int
	ws    []refWall
}

func refNew() refPos {
	return refPos{pawn: [2][2]int{{0, 4}, {8, 4}}, walls: [2]int{10, 10}}
}

func in9(r, c int) bool { return r >= 0 && r < 9 && c >= 0 && c < 9 }

// blocked: заблокировано ли ребро между соседними клетками (a,b).
func (p *refPos) blocked(r1, c1, r2, c2 int) bool {
	for _, w := range p.ws {
		if r1 != r2 { // вертикальное перемещение, мешает горизонтальная стена
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

func (p *refPos) reach(player int) bool {
	var seen [9][9]bool
	var dfs func(r, c int) bool
	dfs = func(r, c int) bool {
		if seen[r][c] {
			return false
		}
		seen[r][c] = true
		goal := 8
		if player == 1 {
			goal = 0
		}
		if r == goal {
			return true
		}
		for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nr, nc := r+d[0], c+d[1]
			if in9(nr, nc) && !p.blocked(r, c, nr, nc) && dfs(nr, nc) {
				return true
			}
		}
		return false
	}
	return dfs(p.pawn[player][0], p.pawn[player][1])
}

func (p *refPos) wallLegal(w refWall) bool {
	if p.walls[p.turn] == 0 || w.r < 0 || w.r > 7 || w.c < 0 || w.c > 7 {
		return false
	}
	for _, o := range p.ws {
		if o.h == w.h {
			if w.h && o.r == w.r && abs(o.c-w.c) <= 1 {
				return false
			}
			if !w.h && o.c == w.c && abs(o.r-w.r) <= 1 {
				return false
			}
		} else if o.r == w.r && o.c == w.c {
			return false
		}
	}
	q := *p
	q.ws = append(append([]refWall{}, p.ws...), w)
	return q.reach(0) && q.reach(1)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (p *refPos) won() bool {
	return p.pawn[0][0] == 8 || p.pawn[1][0] == 0
}

func (p *refPos) moves() []Move {
	var out []Move
	if p.won() {
		return out
	}
	me, op := p.pawn[p.turn], p.pawn[1-p.turn]
	dirs := [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	for _, d := range dirs {
		nr, nc := me[0]+d[0], me[1]+d[1]
		if !in9(nr, nc) || p.blocked(me[0], me[1], nr, nc) {
			continue
		}
		if nr != op[0] || nc != op[1] {
			out = append(out, PawnMove(nr*9+nc))
			continue
		}
		jr, jc := nr+d[0], nc+d[1]
		if in9(jr, jc) && !p.blocked(nr, nc, jr, jc) {
			out = append(out, PawnMove(jr*9+jc))
			continue
		}
		for _, s := range dirs {
			if s[0]*d[0]+s[1]*d[1] != 0 { // только перпендикулярные
				continue
			}
			tr, tc := nr+s[0], nc+s[1]
			if in9(tr, tc) && !p.blocked(nr, nc, tr, tc) {
				out = append(out, PawnMove(tr*9+tc))
			}
		}
	}
	for r := 0; r < 8; r++ {
		for c := 0; c < 8; c++ {
			for _, h := range []bool{true, false} {
				if p.wallLegal(refWall{h, r, c}) {
					out = append(out, WallMove(h, r, c))
				}
			}
		}
	}
	return out
}

func (p refPos) apply(m Move) refPos {
	p.ws = append([]refWall{}, p.ws...)
	switch m.Kind() {
	case KindPawn:
		p.pawn[p.turn] = [2]int{m.Idx() / 9, m.Idx() % 9}
	default:
		p.ws = append(p.ws, refWall{m.Kind() == KindH, m.Idx() / 8, m.Idx() % 8})
		p.walls[p.turn]--
	}
	p.turn = 1 - p.turn
	return p
}

func refPerft(p refPos, d int) uint64 {
	if d == 0 {
		return 1
	}
	ms := p.moves()
	if d == 1 {
		return uint64(len(ms))
	}
	var n uint64
	for _, m := range ms {
		n += refPerft(p.apply(m), d-1)
	}
	return n
}

func sorted(ms []Move) []Move {
	s := append([]Move{}, ms...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s
}

func equalMoves(a, b []Move) bool {
	a, b = sorted(a), sorted(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPerftStart(t *testing.T) {
	b := New()
	if got := Perft(b, 1); got != 131 { // 3 хода пешкой + 128 стен
		t.Fatalf("perft1 = %d, want 131", got)
	}
}

func TestPerftVsReference(t *testing.T) {
	depth := 3
	if testing.Short() {
		depth = 2
	}
	for d := 1; d <= depth; d++ {
		got, want := Perft(New(), d), refPerft(refNew(), d)
		t.Logf("perft(%d) = %d (ref %d)", d, got, want)
		if got != want {
			t.Fatalf("perft(%d): board=%d ref=%d", d, got, want)
		}
	}
}

// Случайные партии: на каждой позиции наборы ходов должны совпасть.
func TestRandomGamesVsReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	games, plies := 300, 120
	if testing.Short() {
		games = 40
	}
	for g := 0; g < games; g++ {
		b, p := New(), refNew()
		for ply := 0; ply < plies; ply++ {
			bm := b.Moves(nil)
			rm := p.moves()
			if !equalMoves(bm, rm) {
				t.Fatalf("game %d ply %d: move sets differ\nboard=%v\nref=%v\nB=%+v", g, ply, sorted(bm), sorted(rm), b)
			}
			if (b.Winner() >= 0) != p.won() {
				t.Fatalf("winner mismatch game %d ply %d", g, ply)
			}
			if len(bm) == 0 {
				break
			}
			// чаще ходим пешкой, чтобы партии доходили до конца
			var m Move
			if rng.Intn(100) < 55 {
				var pm []Move
				for _, x := range bm {
					if x.Kind() == KindPawn {
						pm = append(pm, x)
					}
				}
				if len(pm) > 0 {
					m = pm[rng.Intn(len(pm))]
				} else {
					m = bm[rng.Intn(len(bm))]
				}
			} else {
				m = bm[rng.Intn(len(bm))]
			}
			b, p = b.Apply(m), p.apply(m)
			if int(b.Turn) != p.turn || int(b.P[0]) != p.pawn[0][0]*9+p.pawn[0][1] ||
				int(b.P[1]) != p.pawn[1][0]*9+p.pawn[1][1] || b.Walls != [2]uint8{uint8(p.walls[0]), uint8(p.walls[1])} {
				t.Fatalf("state diverged game %d ply %d", g, ply)
			}
		}
	}
}

