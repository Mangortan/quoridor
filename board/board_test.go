package board

import (
    "testing"
)

func TestStartDistances(t *testing.T) {
	b := New()
	if d := b.Dist(8)[b.P[0]]; d != 8 {
		t.Fatalf("player0 dist = %d, want 8", d)
	}
	if d := b.Dist(0)[b.P[1]]; d != 8 {
		t.Fatalf("player1 dist = %d, want 8", d)
	}
}

func TestStartPawnMoves(t *testing.T) {
	b := New()
	m := b.PawnMoves(0, make([]uint8, 0, 8))
	if len(m) != 3 { // из верхнего края: вниз, влево, вправо
		t.Fatalf("got %d moves, want 3", len(m))
	}
}

func TestStraightJump(t *testing.T) {
	b := New()
	b.P = [2]uint8{4*9 + 4, 5*9 + 4} // соперник прямо под нами
	found := false
	for _, m := range b.PawnMoves(0, make([]uint8, 0, 8)) {
		if int(m) == 6*9+4 {
			found = true
		}
	}
	if !found {
		t.Fatal("straight jump missing")
	}
}

func TestSideJumpWhenBlocked(t *testing.T) {
	b := New()
	b.P = [2]uint8{4*9 + 4, 5*9 + 4}
	// стена под соперником (между строками 5 и 6, столбцы 4-5)
	b.HW |= 1 << uint(5*8+4)
	moves := b.PawnMoves(0, make([]uint8, 0, 8))
	left, right, straight := false, false, false
	for _, m := range moves {
		switch int(m) {
		case 5*9 + 3:
			left = true
		case 5*9 + 5:
			right = true
		case 6*9 + 4:
			straight = true
		}
	}
	if straight || !left || !right {
		t.Fatalf("bad jump set: %v", moves)
	}
}

func TestWallOverlapAndCross(t *testing.T) {
	b := New()
	b2, ok := b.WithWall(true, 3, 3)
	if !ok {
		t.Fatal("first wall must be legal")
	}
	if _, ok := b2.WithWall(true, 3, 4); ok {
		t.Fatal("overlapping wall must be illegal")
	}
	if _, ok := b2.WithWall(false, 3, 3); ok {
		t.Fatal("crossing wall must be illegal")
	}
}
























