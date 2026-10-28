package eval

import (
	"math/rand"
	"testing"

	"quoridor/board"
	"quoridor/movegen"
)

// mirror поворачивает доску на 180° и меняет игроков: позиция «глазами соперника».
func mirror(b board.Board) board.Board {
	var m board.Board
	for i := 0; i < 64; i++ {
		r, c := i/8, i%8
		j := (7-r)*8 + (7 - c)
		if b.HW>>uint(i)&1 != 0 {
			m.HW |= 1 << uint(j)
		}
		if b.VW>>uint(i)&1 != 0 {
			m.VW |= 1 << uint(j)
		}
	}
	for p := 0; p < 2; p++ {
		r, c := int(b.P[p])/9, int(b.P[p])%9
		m.P[1-p] = uint8((8-r)*9 + (8 - c))
		m.Walls[1-p] = b.Walls[p]
	}
	m.Turn = 1 - b.Turn
	return m
}

func TestSymmetry(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	n := 0
	for g := 0; g < 100; g++ {
		b := board.New()
		for ply := 0; ply < 60 && b.Winner() < 0; ply++ {
			if V1(&b) != V1(ptr(mirror(b))) {
				t.Fatalf("asymmetric eval: %d vs %d\n%+v", V1(&b), V1(ptr(mirror(b))), b)
			}
			n++
			ms := movegen.Gen(&b, nil)
			b = b.Apply(ms[rng.Intn(len(ms))])
		}
	}
	t.Logf("checked %d positions", n)
}

func ptr(b board.Board) *board.Board { return &b }

func TestStart(t *testing.T) {
	b := board.New()
	if got := V1(&b); got != 20 {
		t.Fatalf("start eval = %d, want 20 (tempo only)", got)
	}
}

