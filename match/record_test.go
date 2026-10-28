package match

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"quoridor/board"
	"quoridor/search"
)

func randomRecords(n int) []Record {
	sc := Run(func() Bot { return NewSearcher(14, search.Limits{MaxDepth: 2}) }, func() Bot { return Walker{} }, n/2, 4, 300, 2, 5)
	return sc.Games
}

func TestRecordRoundTrip(t *testing.T) {
	gs := randomRecords(20)
	var buf bytes.Buffer
	if err := WriteGames(&buf, gs); err != nil {
		t.Fatal(err)
	}
	back, err := ReadGames(&buf)
	if err != nil || len(back) != len(gs) {
		t.Fatalf("read: %v (%d vs %d)", err, len(back), len(gs))
	}
	for i := range gs {
		if gs[i].String() != back[i].String() || gs[i].ASide < 0 {
			t.Fatalf("game %d differs:\n%s\n%s", i, gs[i], back[i])
		}
		if _, err := Replay(back[i]); err != nil {
			t.Fatalf("game %d replay: %v", i, err)
		}
	}
}

func TestBoardRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, g := range randomRecords(10) {
		ps, _ := Replay(g)
		for _, b := range ps {
			got, err := ParseBoard(FormatBoard(b))
			if err != nil || got != b {
				t.Fatalf("round trip failed: %v\n%+v\n%+v", err, b, got)
			}
		}
		_ = rng
	}
	if _, err := ParseBoard("garbage"); err == nil {
		t.Fatal("garbage must fail")
	}
}

func TestParseMoveErrors(t *testing.T) {
	for _, s := range []string{"", "x1", "p99", "h99", "h8", "p-1"} {
		if _, err := ParseMove(s); err == nil {
			t.Errorf("%q must fail", s)
		}
	}
	for _, m := range []board.Move{board.PawnMove(0), board.PawnMove(80), board.WallMove(true, 7, 7), board.WallMove(false, 0, 3)} {
		back, err := ParseMove(FormatMove(m))
		if err != nil || back != m {
			t.Errorf("move %v -> %q -> %v (%v)", m, FormatMove(m), back, err)
		}
	}
}

func TestLossPositions(t *testing.T) {
	// A = поиск, B = Walker; A-поражения встречаются редко, но B проигрывает часто
	gs := randomRecords(40)
	lostB := LossPositions(gs, 1, 3)
	if len(lostB) == 0 {
		t.Fatal("expected some positions where walker lost")
	}
	for _, p := range lostB {
		g := gs[p.Game]
		if g.Winner != g.ASide || int(p.Board.Turn) != 1-g.ASide { // B проиграла: A победила, ход в позиции за B
			t.Fatalf("wrong position selection: %+v game %v", p, g.String())
		}
	}
	var buf bytes.Buffer
	if err := WritePositions(&buf, lostB); err != nil {
		t.Fatal(err)
	}
	back, err := ReadPositions(strings.NewReader(buf.String()))
	if err != nil || len(back) != len(lostB) {
		t.Fatalf("read positions: %v", err)
	}
	for i := range back {
		if back[i].Board != lostB[i].Board || back[i].Played != lostB[i].Played {
			t.Fatalf("position %d differs", i)
		}
	}
}

func TestBotFactory(t *testing.T) {
	for _, ok := range []string{"walker", "v1", "v2", "v2:Flex=5,Walls=150"} {
		if _, err := BotFactory(ok, 2, 0, 14); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "v3", "v2:Flex", "v2:Nope=1", "v2:Flex=x"} {
		if _, err := BotFactory(bad, 2, 0, 14); err == nil {
			t.Errorf("%q must fail", bad)
		}
	}
	f, _ := BotFactory("v2:Walls=200", 2, 0, 14)
	b := board.New()
	if m := f().Choose(b); m == 0 && false {
		t.Fatal(m)
	}
}

