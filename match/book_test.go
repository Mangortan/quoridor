package match

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quoridor/board"
	"quoridor/path"
	"quoridor/search"
)

func mustMoves(t *testing.T, s string) []board.Move {
	t.Helper()
	var out []board.Move
	for _, f := range strings.Fields(s) {
		m, err := ParseMove(f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func TestBookSideAndWeights(t *testing.T) {
	bk := NewBook()
	bk.NoMirror = true
	line := mustMoves(t, "p13 p67 p22 p58")
	// side=0: записаны только ходы синего (1-й и 3-й полуходы)
	if err := bk.AddLine(line, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := bk.AddLine(line, 0, 0); err != nil {
		t.Fatal(err)
	}
	if bk.Positions() != 2 {
		t.Fatalf("positions=%d, want 2", bk.Positions())
	}
	b := board.New()
	l := bk.Lookup(&b)
	if len(l) != 1 || l[0].Weight != 2 || l[0].Move != line[0] {
		t.Fatalf("lookup root = %+v", l)
	}
	b = b.Apply(line[0])
	if bk.Lookup(&b) != nil {
		t.Fatal("ходы красного не должны попасть в книгу при side=0")
	}
	// обе стороны
	bk2 := NewBook()
	bk2.NoMirror = true
	if err := bk2.AddLine(line, -1, 0); err != nil {
		t.Fatal(err)
	}
	if bk2.Positions() != 4 {
		t.Fatalf("positions=%d, want 4", bk2.Positions())
	}
	// ограничение длины
	bk3 := NewBook()
	bk3.NoMirror = true
	if err := bk3.AddLine(line, -1, 2); err != nil {
		t.Fatal(err)
	}
	if bk3.Positions() != 2 {
		t.Fatalf("maxPlies: positions=%d, want 2", bk3.Positions())
	}
}

func TestBookRejectsIllegal(t *testing.T) {
	bk := NewBook()
	if err := bk.AddLine(mustMoves(t, "p13 p67 p40"), -1, 0); err == nil { // p40 не соседняя клетка
		t.Fatal("illegal pawn move accepted")
	}
	if _, err := ReadBook(strings.NewReader("side=0 p13 p67\nside=2 p13\n")); err == nil || !strings.Contains(err.Error(), "строка 2") {
		t.Fatalf("want line-2 error, got %v", err)
	}
}

func TestReadBookComments(t *testing.T) {
	bk, err := ReadBook(strings.NewReader("# открытие\n\nside=1 p13 p67 p22 p58 # три вперёд\n  side=* p13  \n"))
	if err != nil {
		t.Fatal(err)
	}
	if bk.Lines != 2 {
		t.Fatalf("lines=%d", bk.Lines)
	}
}

type fixedBot struct{ m board.Move }

func (f fixedBot) Choose(board.Board) board.Move { return f.m }

func TestBookBot(t *testing.T) {
	bk := NewBook()
	if err := bk.AddLine(mustMoves(t, "p13 p67"), 0, 0); err != nil {
		t.Fatal(err)
	}
	fallback := mustMoves(t, "p31")[0]
	bb := NewBookBot(bk, fixedBot{fallback})
	b := board.New()
	if m := bb.Choose(b); m != mustMoves(t, "p13")[0] || !bb.FromBook {
		t.Fatalf("expected book move, got %s fromBook=%v", FormatMove(m), bb.FromBook)
	}
	// позиция вне книги — ход внутреннего бота
	b = b.Apply(mustMoves(t, "p13")[0]).Apply(mustMoves(t, "p67")[0])
	if m := bb.Choose(b); m != fallback || bb.FromBook {
		t.Fatalf("expected fallback, got %s fromBook=%v", FormatMove(m), bb.FromBook)
	}
}

func TestBookFactorySpec(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.txt")
	if err := os.WriteFile(path, []byte("side=0 p13 p67 p22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := BotFactory("v2@book="+path, 2, 0, 16)
	if err != nil {
		t.Fatal(err)
	}
	bot := f()
	if m := bot.Choose(board.New()); m != mustMoves(t, "p13")[0] {
		t.Fatalf("got %s", FormatMove(m))
	}
	if SearcherOf(bot) == nil {
		t.Fatal("SearcherOf must unwrap book bot")
	}
	if _, err := BotFactory("v2@book="+filepath.Join(dir, "нет.txt"), 2, 0, 16); err == nil {
		t.Fatal("missing book file must be an error")
	}
	// партия бота с книгой доигрывается до конца
	rec := PlayGame(f(), Walker{}, nil, 300)
	if rec.Winner < 0 {
		t.Fatalf("game unfinished: %v", rec)
	}
}

func TestMirrorMove(t *testing.T) {
	cases := map[string]string{"p13": "p13", "p21": "p23", "p39": "p41", "p40": "p40", "h23": "h24", "h00": "h07", "v13": "v14", "v70": "v77"}
	for in, want := range cases {
		a, _ := ParseMove(in)
		if got := FormatMove(MirrorMove(a)); got != want {
			t.Errorf("mirror %s = %s, want %s", in, got, want)
		}
		if MirrorMove(MirrorMove(a)) != a {
			t.Errorf("mirror not involutive for %s", in)
		}
	}
}

func TestBookMirror(t *testing.T) {
	// линия с несимметричными ходами: p21 (клетка левее центра) и стена h23
	line := mustMoves(t, "p13 p67 p22 p58 p21 p49 h23")
	bk := NewBook()
	if err := bk.AddLine(line, 0, 0); err != nil {
		t.Fatal(err)
	}
	// позиция после зеркальных ходов p13 p67 p26 p58 находит зеркальный ход h24
	b := board.New()
	for _, m := range mustMoves(t, "p13 p67 p22 p58 p23 p49") {
		b = b.Apply(m)
	}
	l := bk.Lookup(&b)
	if len(l) != 1 || FormatMove(l[0].Move) != "h24" {
		t.Fatalf("mirrored lookup = %+v", l)
	}
	// симметричное начало не удваивает вес: корень получил вес 1, а не 2
	root := board.New()
	if l := bk.Lookup(&root); len(l) != 1 || l[0].Weight != 1 {
		t.Fatalf("root = %+v", l)
	}
	// nomirror в файле отключает зеркало для линии
	bk2, err := ReadBook(strings.NewReader("side=0 nomirror p13 p67 p22 p58 p21 p49 h23\n"))
	if err != nil {
		t.Fatal(err)
	}
	if bk2.Lookup(&b) != nil {
		t.Fatal("nomirror line must not be mirrored")
	}
	// отражённая линия всегда легальна
	if _, err := ReadBook(strings.NewReader("p13 p67 h23 h43 v20 p58\n")); err != nil {
		t.Fatal(err)
	}
}

func TestStartsWithAdvance(t *testing.T) {
	// синие: p13 p22 p31 — три шага вперёд (красные между ними ходят как угодно)
	if !StartsWithAdvance(mustMoves(t, "p13 p67 p22 p58 p31 p49"), 0, 3) {
		t.Error("blue 3 advances rejected")
	}
	// стена третьим ходом синих
	if StartsWithAdvance(mustMoves(t, "p13 p67 p22 p58 h23 p49"), 0, 3) {
		t.Error("wall as 3rd move accepted")
	}
	// шаг в сторону не считается
	if StartsWithAdvance(mustMoves(t, "p13 p67 p12 p58 p21 p49"), 0, 3) {
		t.Error("sideways step accepted")
	}
	// красные: p67 p58 p49 — вверх
	if !StartsWithAdvance(mustMoves(t, "p13 p67 p22 p58 p31 p49"), 1, 3) {
		t.Error("red 3 advances rejected")
	}
	// короткая партия и выключенное условие
	if StartsWithAdvance(mustMoves(t, "p13 p67"), 0, 3) {
		t.Error("short game accepted")
	}
	if !StartsWithAdvance(nil, 0, 0) {
		t.Error("n=0 must accept everything")
	}
}

// Ход книги, который поиск считает заметно хуже лучшего, не играется.
func TestBookVerifyRejectsBadMove(t *testing.T) {
	bk := NewBook()
	// бесполезная стена в дальнем углу вместо шага вперёд
	if err := bk.AddLine(mustMoves(t, "h00"), 0, 0); err != nil {
		t.Fatal(err)
	}
	mk := func() *BookBot {
		s := NewSearcher(16, search.Limits{MaxDepth: 3})
		s.S.Eval = func(b board.Board) int { // только разница путей: стена в углу ничего не даёт
			return 100 * (path.Len(&b, 1) - path.Len(&b, 0))
		}
		bb := NewBookBot(bk, s)
		bb.Margin = 50
		return bb
	}
	bb := mk()
	m := bb.Choose(board.New())
	if bb.FromBook || !bb.Rejected || m.Kind() != board.KindPawn {
		t.Fatalf("плохой ход книги должен быть отвергнут: move=%s fromBook=%v rejected=%v", FormatMove(m), bb.FromBook, bb.Rejected)
	}
	// без проверки тот же ход играется
	bb = mk()
	bb.Verify = false
	if m := bb.Choose(board.New()); !bb.FromBook || FormatMove(m) != "h00" {
		t.Fatalf("без проверки ожидали ход книги, получили %s", FormatMove(m))
	}
}

// Результаты: проигрывающий ход отключается, выигрывающий усиливается, статистика переживает перезапуск.
func TestBookResultsLearning(t *testing.T) {
	dir := t.TempDir()
	bookPath := filepath.Join(dir, "book.txt")
	if err := os.WriteFile(bookPath, []byte("side=0 p13\nside=0 h00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := BotFactory("walker@book="+bookPath, 0, 0, 16)
	if err != nil {
		t.Fatal(err)
	}
	root := board.New()
	h := search.Hash(&root)
	pawn, wall := mustMoves(t, "p13")[0], mustMoves(t, "h00")[0]
	// 3 поражения после стены h00 и победа после p13
	for i := 0; i < 3; i++ {
		bb := f().(*BookBot)
		bb.used = []usedMove{{h, wall, 0}}
		bb.Finish(1) // победил красный: синий (side 0) проиграл
	}
	bb := f().(*BookBot)
	bb.used = []usedMove{{h, pawn, 0}}
	bb.Finish(0)
	if st := bb.Book.stat(h, wall); st.L != 3 || !st.banned() {
		t.Fatalf("стена после 3 поражений: %+v banned=%v", st, st.banned())
	}
	// заново загруженная книга помнит результаты и больше не играет h00
	f2, _ := BotFactory("walker@book="+bookPath, 0, 0, 16)
	for i := 0; i < 30; i++ {
		if m := f2().Choose(root); m == wall {
			t.Fatal("ход с плохими результатами сыгран снова")
		}
	}
	if st := f2().(*BookBot).Book.stat(h, pawn); st.W != 1 {
		t.Fatalf("победа не сохранилась: %+v", st)
	}
	if data, _ := os.ReadFile(bookPath + ".stats"); strings.Count(string(data), "\n") != 4 {
		t.Fatalf("stats file: %q", data)
	}
}

// Проверка линий самоигрой отличает проигрышный дебют от выигрышного: синие потеряли два темпа (пошли назад),
// поэтому у ходоков-одинаковых ботов синие проигрывают, а красные выигрывают.
func TestValidateLines(t *testing.T) {
	line := mustMoves(t, "p13 p67 p4 p58 p13 p49")
	jobs := []ValidateJob{{Moves: line, Side: 0}, {Moves: line, Side: 1}}
	stats := ValidateLines(func() Bot { return Walker{} }, jobs, 6, 0, 300, 2, 1, nil)
	if stats[0].Losses != 6 || stats[0].Score() != 0 {
		t.Fatalf("owner blue: %+v", stats[0])
	}
	if stats[1].Wins != 6 || stats[1].Score() != 1 {
		t.Fatalf("owner red: %+v", stats[1])
	}
}

