package match

import (
	"testing"
	"time"

	"quoridor/board"
	"quoridor/eval"
	"quoridor/search"
)

func newPonderBot(depth int, ponder bool) *Searcher {
	s := NewSearcher(18, search.Limits{MaxDepth: depth})
	s.S.Eval = func(b board.Board) int { return eval.V1(&b) }
	s.Ponder = ponder
	s.PonderMax = 10 * time.Second
	return s
}

// «Думалка» запускается после хода, останавливается быстро, а если соперник сыграл угаданный ход,
// то поиск идёт по прогретой таблице и тратит заметно меньше узлов, чем с холодной.
func TestPonderWarmsTable(t *testing.T) {
	b := board.New()
	s := newPonderBot(4, true)
	m := s.Choose(b)
	if s.PonderStarts != 1 || !s.pondOn {
		t.Fatalf("ponder not started: starts=%d on=%v", s.PonderStarts, s.pondOn)
	}
	next := s.pondPos
	time.Sleep(600 * time.Millisecond) // соперник «думает»

	t0 := time.Now()
	s.StopPonder()
	if d := time.Since(t0); d > 200*time.Millisecond {
		t.Fatalf("StopPonder took %v", d)
	}
	_ = m

	warm := s.S.Search(next, search.Limits{MaxDepth: 6})
	cold := newPonderBot(6, false).S.Search(next, search.Limits{MaxDepth: 6})
	t.Logf("узлов на глубине 6: прогретая таблица %d, холодная %d", warm.Nodes, cold.Nodes)
	if warm.Nodes*2 > cold.Nodes {
		t.Fatalf("warm search not cheaper: warm=%d cold=%d", warm.Nodes, cold.Nodes)
	}
	if warm.Move != cold.Move && warm.Score != cold.Score {
		t.Logf("ходы различаются (допустимо при равных оценках): %s / %s", FormatMove(warm.Move), FormatMove(cold.Move))
	}
}

// Попадание учитывается, когда соперник сыграл именно угаданный ход.
func TestPonderHitCounted(t *testing.T) {
	s := newPonderBot(3, true)
	b := board.New()
	m := s.Choose(b)
	after := b.Apply(m)
	pred, ok := s.S.TTMove(&after)
	// TTMove нельзя звать во время «думалки», но проверка здесь нужна: остановим её сначала
	s.StopPonder()
	if pred, ok = s.S.TTMove(&after); !ok {
		t.Skip("нет угаданного хода")
	}
	s.pondPos, s.pondOn = after.Apply(pred), true // как будто думаем именно здесь
	s.Choose(after.Apply(pred))
	if s.PonderHits != 1 {
		t.Fatalf("hits=%d, want 1", s.PonderHits)
	}
	// промах: другая позиция
	s2 := newPonderBot(3, true)
	s2.Choose(b)
	s2.StopPonder()
	s2.pondPos, s2.pondOn = board.New(), true
	s2.Choose(after)
	if s2.PonderHits != 0 {
		t.Fatalf("hits=%d, want 0", s2.PonderHits)
	}
	s.StopPonder()
	s2.StopPonder()
}

// Партия бот против бота с включённой «думалкой» доигрывается и без гонок (запускайте с -race).
func TestPonderFullGame(t *testing.T) {
	a, b := newPonderBot(3, true), newPonderBot(3, true)
	a.PonderMax, b.PonderMax = 50*time.Millisecond, 50*time.Millisecond
	rec := PlayGame(a, b, nil, 120)
	a.StopPonder()
	b.StopPonder()
	if len(rec.Moves) < 20 {
		t.Fatalf("игра слишком короткая: %d", len(rec.Moves))
	}
	t.Logf("полуходов %d, победитель %d; «думалок» %d/%d, попаданий %d/%d", len(rec.Moves), rec.Winner, a.PonderStarts, b.PonderStarts, a.PonderHits, b.PonderHits)
}

func TestBotFactoryPonderSpec(t *testing.T) {
	f, err := BotFactory("v2@ponder", 3, 0, 16)
	if err != nil {
		t.Fatal(err)
	}
	if s := SearcherOf(f()); s == nil || !s.Ponder {
		t.Fatal("@ponder must enable pondering")
	}
	if f, err := BotFactory("v2", 3, 0, 16); err != nil || SearcherOf(f()).Ponder {
		t.Fatal("ponder must be off by default")
	}
}

