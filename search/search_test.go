package search

import (
	"math/rand"
	"testing"
	"time"

	"quoridor/board"
	"quoridor/eval"
	"quoridor/movegen"
)

func positions(n int, seed int64) []board.Board {
	rng := rand.New(rand.NewSource(seed))
	var out []board.Board
	for len(out) < n {
		b := board.New()
		for ply := 0; ply < 100 && len(out) < n && b.Winner() < 0; ply++ {
			out = append(out, b)
			ms := movegen.Gen(&b, nil)
			b = b.Apply(ms[rng.Intn(len(ms))])
		}
	}
	return out
}

// Эталон: обычный минимакс без отсечений и TT, с тем же правилом повторов.
func refMinimax(b *board.Board, depth, ply int, keys *[maxPly + 1]uint64) int {
	if b.Winner() >= 0 {
		return -(Mate - ply)
	}
	key := Hash(b)
	if ply > 0 {
		for i := ply - 2; i >= 0; i -= 2 {
			if keys[i] == key {
				return 0
			}
		}
	}
	keys[ply] = key
	if depth <= 0 {
		return eval.V1(b)
	}
	ms := movegen.Gen(b, nil)
	if len(ms) == 0 {
		return eval.V1(b)
	}
	best := -Inf
	for _, m := range ms {
		nb := b.Apply(m)
		if v := -refMinimax(&nb, depth-1, ply+1, keys); v > best {
			best = v
		}
	}
	return best
}

func TestMatchesPlainMinimax(t *testing.T) {
	n, maxD := 60, 3
	if testing.Short() {
		n = 15
	}
	s := New(18)
	for i, b := range positions(n, 1) {
		for d := 1; d <= maxD; d++ {
			s.Clear()
			got := s.Search(b, Limits{MaxDepth: d})
			var keys [maxPly + 1]uint64
			want := refMinimax(&b, d, 0, &keys)
			if got.Score != want || got.Depth != d {
				t.Fatalf("pos %d depth %d: search=%d (depth %d) minimax=%d\n%+v", i, d, got.Score, got.Depth, want, b)
			}
			// ход должен быть легальным
			if !inMoves(b.Moves(nil), got.Move) {
				t.Fatalf("pos %d depth %d: illegal best move %v", i, d, got.Move)
			}
		}
	}
}

func inMoves(ms []board.Move, m board.Move) bool {
	for _, x := range ms {
		if x == m {
			return true
		}
	}
	return false
}

func TestTakesImmediateWin(t *testing.T) {
	b := board.New()
	b.P = [2]uint8{7*9 + 4, 8*9 + 0}
	r := New(16).Search(b, Limits{MaxDepth: 4})
	if r.Move != board.PawnMove(8*9+4) || r.Score != Mate-1 {
		t.Fatalf("got %+v", r)
	}
}

func TestAvoidsLosingRace(t *testing.T) {
	// Соперник на шаг от цели и его ход следующий: нужно либо выиграть сразу, либо поставить стену.
	b := board.New()
	b.P = [2]uint8{3*9 + 4, 1*9 + 4}
	b.Turn = 0
	r := New(18).Search(b, Limits{MaxDepth: 6})
	if r.Move.Kind() == board.KindPawn {
		t.Fatalf("must block with a wall, got pawn move %v (score %d)", r.Move, r.Score)
	}
}

func TestTimeLimit(t *testing.T) {
	s := New(20)
	start := time.Now()
	r := s.Search(board.New(), Limits{Time: 200 * time.Millisecond})
	el := time.Since(start)
	if el > 400*time.Millisecond {
		t.Fatalf("overran time: %v", el)
	}
	t.Logf("200ms: depth %d, nodes %d, %.0f knps", r.Depth, r.Nodes, float64(r.Nodes)/r.Elapsed.Seconds()/1000)
}

// Приёмка: глубина >= 6 за секунду и 0 аллокаций в поиске.
func TestDepthInOneSecond(t *testing.T) {
	s := New(20)
	ps := positions(200, 5)
	var minD, sumD, cnt int = 99, 0, 0
	for i := 0; i < len(ps); i += 20 { // 10 разных позиций
		s.Clear()
		r := s.Search(ps[i], Limits{Time: time.Second})
		t.Logf("pos %3d: depth %2d nodes %7d %.0f knps", i, r.Depth, r.Nodes, float64(r.Nodes)/r.Elapsed.Seconds()/1000)
		if i == 0 && r.Depth < 6 { // позиция 0 = старт партии
			t.Errorf("start position: depth %d < 6 in 1s", r.Depth)
		}
		if r.Depth < minD {
			minD = r.Depth
		}
		sumD += r.Depth
		cnt++
	}
	t.Logf("min depth %d, avg %.1f", minD, float64(sumD)/float64(cnt))
}

func TestNoAllocs(t *testing.T) {
	s := New(18)
	b := positions(50, 6)[30]
	s.Search(b, Limits{MaxDepth: 4}) // прогрев
	if a := testing.AllocsPerRun(5, func() { s.Search(b, Limits{MaxDepth: 5}) }); a != 0 {
		t.Fatalf("allocs per search = %v, want 0", a)
	}
}

func BenchmarkSearchDepth5(bm *testing.B) {
	s := New(20)
	ps := positions(100, 7)
	bm.ResetTimer()
	for i := 0; i < bm.N; i++ {
		s.Clear()
		s.Search(ps[(i*7)%len(ps)], Limits{MaxDepth: 5})
	}
}

func TestExtensionNoAllocsAndLegal(t *testing.T) {
	s := New(18)
	s.ExtendWalls = true
	ps := positions(40, 9)
	for i, b := range ps {
		r := s.Search(b, Limits{MaxDepth: 3})
		if !inMoves(b.Moves(nil), r.Move) {
			t.Fatalf("pos %d: illegal move with extensions", i)
		}
	}
	b := ps[30]
	s.Search(b, Limits{MaxDepth: 4})
	if a := testing.AllocsPerRun(5, func() { s.Search(b, Limits{MaxDepth: 4}) }); a != 0 {
		t.Fatalf("allocs with extensions = %v", a)
	}
}

func TestHistoryRepetitionIsDraw(t *testing.T) {
	s := New(16)
	b0 := board.New()
	b1 := b0.Apply(board.PawnMove(1*9 + 4))
	b2 := b1.Apply(board.PawnMove(7*9 + 4))
	b3 := b2.Apply(board.PawnMove(0*9 + 4))
	// P1 вернулся бы в b0-подобную позицию: ключи b0 в истории -> повторение
	s.SetHistory([]uint64{Hash(&b0), Hash(&b1)})
	if !s.inHistory(Hash(&b0)) || !s.inHistory(Hash(&b1)) || s.inHistory(Hash(&b2)) {
		t.Fatal("history lookup is wrong")
	}
	s.SetHistory(nil)
	if s.inHistory(Hash(&b0)) {
		t.Fatal("history must be cleared")
	}
	// переход в позицию из истории оценивается как ничья, а не оценкой
	b4 := b3.Apply(board.PawnMove(8*9 + 4)) // == b0 по расстановке и очереди
	if Hash(&b4) != Hash(&b0) {
		t.Fatal("test setup: b4 must equal b0")
	}
	s.SetHistory([]uint64{Hash(&b0)})
	s.Clear()
	for _, m := range movegenMoves(&b3) {
		if m == board.PawnMove(8*9+4) {
			s.boards[0] = b3
			s.keys[0] = Hash(&b3)
			s.boards[1] = b3.Apply(m)
			if v := s.negamax(&s.boards[1], 3, -Inf, Inf, 1); v != 0 {
				t.Fatalf("repeated position must score 0, got %d", v)
			}
		}
	}
	// без аллокаций
	big := make([]uint64, 600)
	for i := range big {
		big[i] = uint64(i)*2654435761 + 1
	}
	if a := testing.AllocsPerRun(5, func() { s.SetHistory(big) }); a != 0 {
		t.Fatalf("SetHistory allocs = %v", a)
	}
}

func movegenMoves(b *board.Board) []board.Move { return movegen.Gen(b, nil) }

