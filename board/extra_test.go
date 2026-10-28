package board

import "testing"

func has2(m []uint8, v int) bool {
	for _, x := range m {
		if int(x) == v {
			return true
		}
	}
	return false
}

func TestEdgeJump(t *testing.T) { // соперник у края, прыжок прямо невозможен -> боковые
	b := New()
	b.P = [2]uint8{7*9 + 4, 8*9 + 4}
	m := b.PawnMoves(0, make([]uint8, 0, 8))
	if has2(m, 9*9+4) || !has2(m, 8*9+3) || !has2(m, 8*9+5) {
		t.Fatalf("edge: %v", m)
	}
}
func TestWallBetweenNoJump(t *testing.T) {
	b := New()
	b.P = [2]uint8{4*9 + 4, 5*9 + 4}
	b.HW |= 1 << uint(4*8+4) // стена между нами
	m := b.PawnMoves(0, make([]uint8, 0, 8))
	if has2(m, 5*9+4) || has2(m, 6*9+4) {
		t.Fatalf("%v", m)
	}
}
func TestSideBlockedToo(t *testing.T) {
	b := New()
	b.P = [2]uint8{4*9 + 4, 5*9 + 4}
	b.HW |= 1 << uint(5*8+4)
	b.VW |= 1 << uint(5*8+3) // слева от соперника
	m := b.PawnMoves(0, make([]uint8, 0, 8))
	if has2(m, 5*9+3) || !has2(m, 5*9+5) {
		t.Fatalf("%v", m)
	}
}
func TestPlayer1Moves(t *testing.T) {
	b := New()
	if m := b.PawnMoves(1, make([]uint8, 0, 8)); len(m) != 3 {
		t.Fatalf("%v", m)
	}
}
func TestSealIllegal(t *testing.T) {
	b := New()
	b.P[0] = 0
	b.HW |= 1 << 0
	if _, ok := b.WithWall(false, 0, 1); ok {
		t.Fatal("sealing wall must be illegal")
	}
}
func TestNoWalls(t *testing.T) {
	b := New()
	b.Walls[0] = 0
	if _, ok := b.WithWall(true, 3, 3); ok {
		t.Fatal("no walls left")
	}
}
func TestTurnFlip(t *testing.T) {
	b := New()
	b2, _ := b.WithWall(true, 3, 3)
	t.Logf("Turn after wall = %d (before %d)", b2.Turn, b.Turn)
}
func TestOverlapOtherSide(t *testing.T) {
	b := New()
	b2, _ := b.WithWall(true, 3, 3)
	if _, ok := b2.WithWall(true, 3, 2); ok {
		t.Fatal("left overlap")
	}
	if _, ok := b2.WithWall(true, 3, 5); !ok {
		t.Fatal("adjacent must be ok")
	}
	b3, _ := b.WithWall(false, 3, 3)
	if _, ok := b3.WithWall(false, 4, 3); ok {
		t.Fatal("vert overlap")
	}
	if _, ok := b3.WithWall(false, 2, 3); ok {
		t.Fatal("vert overlap up")
	}
}

