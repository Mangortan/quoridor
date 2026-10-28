package match

import (
	"runtime"
	"testing"

	"quoridor/board"
	"quoridor/eval"
	"quoridor/search"
)

func workers() int {
	w := runtime.GOMAXPROCS(0)
	if w < 1 {
		w = 1
	}
	return w
}

// Приёмка этапа 5: бот с eval v1 уверенно обыгрывает Walker.
func TestV1BeatsWalker(t *testing.T) {
	pairs := 60
	if testing.Short() {
		pairs = 8
	}
	newA := func() Bot { return NewSearcher(18, search.Limits{MaxDepth: 4}) }
	newB := func() Bot { return Walker{} }
	sc := Run(newA, newB, pairs, 2, 300, workers(), 1) // дебют: 2 случайных полухода
	t.Logf("search(d4) vs walker: +%d -%d =%d  rate %.3f  (%v)", sc.AWins, sc.BWins, sc.Draws, sc.Rate(), sc.Elapsed)
	if sc.Rate() < 0.9 {
		t.Errorf("expected >= 90%% points, got %.1f%%", 100*sc.Rate())
	}
}

// Контроль арены: Walker против Walker — выигрывает тот, кто ходит первым.
func TestWalkerMirror(t *testing.T) {
	sc := Run(func() Bot { return Walker{} }, func() Bot { return Walker{} }, 10, 4, 300, workers(), 2)
	t.Logf("walker vs walker: +%d -%d =%d", sc.AWins, sc.BWins, sc.Draws)
	if sc.Draws != 0 {
		t.Errorf("walkers must always finish, draws=%d", sc.Draws)
	}
}

func v2bot(depth int) Bot {
	s := NewSearcher(18, search.Limits{MaxDepth: depth})
	p := eval.DefaultV2()
	s.S.Eval = p.Eval
	return s
}

func v1bot(depth int) Bot {
	s := NewSearcher(18, search.Limits{MaxDepth: depth})
	s.S.Eval = func(b board.Board) int { return eval.V1(&b) }
	return s
}

// Приёмка этапа 6: v2 выигрывает у v1 со статистической значимостью (SPRT: H0 = 0 Elo, H1 = +30 Elo, alpha=beta=5%).
func TestV2BeatsV1(t *testing.T) {
	if testing.Short() {
		t.Skip("долгий матч")
	}
	r := RunSPRT(func() Bot { return v2bot(3) }, func() Bot { return v1bot(3) },
		SPRT{Elo0: 0, Elo1: 30, Alpha: 0.05, Beta: 0.05}, 300, 50, 4, 300, workers(), 61)
	t.Logf("v2 vs v1 (depth 3): %s (%v)", r, r.Score.Elapsed.Round(1e8))
	if r.Verdict != "H1" {
		t.Errorf("v2 does not beat v1 significantly: %s", r)
	}
}

func TestSPRTMath(t *testing.T) {
	s := SPRT{Elo0: 0, Elo1: 30, Alpha: 0.05, Beta: 0.05}
	if l := s.LLR(300, 0, 200); l <= 0 {
		t.Errorf("better score must give positive LLR, got %v", l)
	}
	if l := s.LLR(200, 0, 300); l >= 0 {
		t.Errorf("worse score must give negative LLR, got %v", l)
	}
	lo, hi := s.Bounds()
	if lo >= 0 || hi <= 0 {
		t.Errorf("bounds %v %v", lo, hi)
	}
	e, a, b := EloCI(55, 0, 45)
	if !(a < e && e < b) || e < 30 || e > 40 {
		t.Errorf("EloCI(55,0,45) = %.1f [%.1f %.1f]", e, a, b)
	}
}

// playFrom доигрывает партию из b как PlayGame: с историей позиций и ничьей при троекратном повторении.
// Возвращает победителя (-1 = ничья по повторению или лимиту) и число полуходов.
func playFrom(b board.Board, noEndgame bool, maxPlies int) (winner, plies int) {
	bots := [2]*Searcher{NewSearcher(16, search.Limits{MaxDepth: 3}), NewSearcher(16, search.Limits{MaxDepth: 3})}
	for _, s := range bots {
		s.S.NoEndgame = noEndgame
	}
	var keys []uint64
	count := map[uint64]int{}
	for plies < maxPlies {
		if w := b.Winner(); w >= 0 {
			return w, plies
		}
		k := search.Hash(&b)
		if count[k] >= 2 {
			return -1, plies
		}
		bots[b.Turn].SetHistory(keys)
		keys = append(keys, k)
		count[k]++
		b = b.Apply(bots[b.Turn^0].Choose(b))
		plies++
	}
	return -1, plies
}

// Регрессия: позиция из реальной партии, где у обоих кончились стены, а боты ходили туда-сюда до ничьей.
// Теперь оба играют точным эндшпилем и доводят партию до победы.
func TestEndgameNoShuffle(t *testing.T) {
	b, err := ParseBoard("HW=0xaa540200205500 VW=0x18824900000 P=56,48 W=0,0 T=0")
	if err != nil {
		t.Fatal(err)
	}
	w0, n0 := playFrom(b, true, 200)
	w1, n1 := playFrom(b, false, 200)
	t.Logf("без эндшпиля: победитель %d, %d полуходов; с эндшпилем: победитель %d, %d полуходов", w0, n0, w1, n1)
	if w1 < 0 {
		t.Fatalf("с эндшпилем партия не закончилась победой (%d полуходов)", n1)
	}
}

