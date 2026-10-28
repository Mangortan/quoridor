package threat

import (
	"testing"
	"time"

	"quoridor/board"
	"quoridor/path"
)

// Регрессионные позиции: двухстеночные ловушки, где ни одна стена сама не даёт gain >= 2,
// но после первой стены у соперника нет ответа, который снимает угрозу.
// (Позиции получены полным перебором FindExhaustive и зафиксированы здесь.)
var traps = []board.Board{
	{HW: 0x800140010100024, VW: 0x0, P: [2]uint8{4, 67}, Walls: [2]uint8{6, 7}, Turn: 0},
	{HW: 0x800140010101024, VW: 0x400000000000000, P: [2]uint8{4, 67}, Walls: [2]uint8{5, 6}, Turn: 0},
	{HW: 0xa08282000000000, VW: 0x1000000000000008, P: [2]uint8{4, 76}, Walls: [2]uint8{6, 6}, Turn: 0},
	{HW: 0xa08292000000000, VW: 0x1000000000000008, P: [2]uint8{4, 76}, Walls: [2]uint8{5, 6}, Turn: 1},
	{HW: 0xa09292008000000, VW: 0x1000000000000008, P: [2]uint8{4, 76}, Walls: [2]uint8{4, 5}, Turn: 1},
	{HW: 0x40400000140000, VW: 0x1800001000000012, P: [2]uint8{4, 76}, Walls: [2]uint8{5, 6}, Turn: 1},
	{HW: 0x40400000540000, VW: 0x1800001200000012, P: [2]uint8{4, 76}, Walls: [2]uint8{4, 5}, Turn: 1},
	{HW: 0x800104405000890, VW: 0x200000000000, P: [2]uint8{4, 76}, Walls: [2]uint8{5, 5}, Turn: 0},
}

// Независимая проверка ловушки: полный перебор ходов через board.Moves (медленный эталон правил).
func verifyTrap(t *testing.T, b board.Board, w board.Move, thr int) {
	t.Helper()
	me := int(b.Turn)
	d0 := path.Len(&b, 1-me) - path.Len(&b, me)
	nb := b.Apply(w)
	for _, r := range nb.Moves(nil) {
		n2 := nb.Apply(r)
		if n2.Winner() >= 0 {
			t.Fatalf("opponent wins at once after trap wall, reply %v", r)
		}
		ok := false
		for _, w2 := range n2.Moves(nil) {
			if w2.Kind() == board.KindPawn {
				continue
			}
			n3 := n2.Apply(w2)
			if path.Len(&n3, 1-me)-path.Len(&n3, me)-d0-1 >= thr {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("reply %v refutes the trap wall %v", r, w)
		}
	}
}

func TestRegressionTraps(t *testing.T) {
	for i, b := range traps {
		if one := Find(&b, 1, 2); one.Found {
			t.Fatalf("trap %d: single wall already wins (not a pure 2-wall trap)", i)
		}
		r := Find(&b, 2, 2)
		if !r.Found {
			t.Fatalf("trap %d: Find missed the trap", i)
		}
		verifyTrap(t, b, r.Move, 2)
	}
}

// Найденные на случайных позициях ловушки независимо подтверждаются board.Moves.
func TestFoundTrapsVerified(t *testing.T) {
	n, checked := 400, 0
	if testing.Short() {
		n = 100
	}
	for _, b := range positions(n, 4) {
		r := Find(&b, 2, 2)
		if r.Found {
			verifyTrap(t, b, r.Move, 2)
			checked++
		}
	}
	t.Logf("independently verified %d traps", checked)
}

func TestTiming(t *testing.T) {
	ps := positions(200, 5)
	depths := []int{1, 2, 3}
	if testing.Short() {
		depths, ps = []int{1, 2}, ps[:50]
	}
	for _, d := range depths {
		t0 := time.Now()
		found := 0
		for _, b := range ps {
			if Find(&b, d, 2).Found {
				found++
			}
		}
		el := time.Since(t0)
		t.Logf("depth %d: %d/%d positions with a forced win-of-path, %v per position", d, found, len(ps), el/time.Duration(len(ps)))
	}
}

