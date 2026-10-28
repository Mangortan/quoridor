package match

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"quoridor/board"
	"quoridor/movegen"
	"quoridor/search"
)

// Книга дебютов — набор линий (начал партий). Формат текстового файла, одна линия на строку:
//
//	side=0 p13 p67 p22 p58 p31 p49 h23   # необязательный комментарий
//
// side — чьи ходы брать из линии в книгу: 0 (синий), 1 (красный) или * (обе стороны, по умолчанию).
// Остальное — ходы с начала партии в том же формате, что и в файлах партий (pNN, hRC, vRC).
// Книга ключуется ПОЗИЦИЕЙ (а не номером хода), поэтому перестановки ходов работают сами.
// Если одну и ту же позицию с одним ходом записали несколько раз, ход получает больший вес.

type BookMove struct {
	Move   board.Move
	Weight int
}

type Book struct {
	m     map[uint64][]BookMove
	Lines int
	// NoMirror выключает зеркалирование: по умолчанию каждая линия записывается вместе с зеркальной
	// (слева направо, колонка c -> 8-c; для стен c -> 7-c), так что дебют работает и «в другую сторону».
	NoMirror bool

	mu        sync.RWMutex
	stats     map[bookKey]Stat // результаты партий по ходам книги (см. BookBot.Finish)
	statsFile string           // сюда дописываются новые результаты
}

type bookKey struct {
	h uint64
	m board.Move
}

// Stat — итоги партий, в которых бот сыграл этот ход из книги.
type Stat struct{ W, L, D int }

func (st Stat) n() int { return st.W + st.L + st.D }

// Rate — доля очков с поправкой на малую выборку (0.5 при отсутствии данных).
func (st Stat) Rate() float64 { return (float64(st.W) + 0.5*float64(st.D) + 1) / (float64(st.n()) + 2) }

// banned: ход уже пробовали несколько раз, и он почти всегда проигрывал.
func (st Stat) banned() bool { return st.n() >= 3 && st.Rate() < 0.3 }

// factor — множитель веса хода по накопленным результатам: 1 без данных, до 2 для выигрывающих ходов.
func (st Stat) factor() float64 { return 2 * st.Rate() }

func (bk *Book) stat(h uint64, m board.Move) Stat {
	bk.mu.RLock()
	defer bk.mu.RUnlock()
	return bk.stats[bookKey{h, m}]
}

// AddResult учитывает исход партии для хода m в позиции с ключом h; res: 'W', 'L' или 'D'.
func (bk *Book) AddResult(h uint64, m board.Move, res byte) {
	bk.mu.Lock()
	defer bk.mu.Unlock()
	if bk.stats == nil {
		bk.stats = map[bookKey]Stat{}
	}
	st := bk.stats[bookKey{h, m}]
	switch res {
	case 'W':
		st.W++
	case 'L':
		st.L++
	default:
		st.D++
	}
	bk.stats[bookKey{h, m}] = st
	if bk.statsFile != "" {
		if f, err := os.OpenFile(bk.statsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(f, "%016x %s %c\n", h, FormatMove(m), res)
			f.Close()
		}
	}
}

// LoadStats читает файл результатов (строки "ключ ход W|L|D"), накопленные прошлыми партиями.
func (bk *Book) LoadStats(r io.Reader) error {
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) != 3 || len(f[2]) != 1 {
			return fmt.Errorf("строка %d: ожидается «ключ ход W|L|D»", n)
		}
		h, err := strconv.ParseUint(f[0], 16, 64)
		if err != nil {
			return fmt.Errorf("строка %d: %v", n, err)
		}
		m, err := ParseMove(f[1])
		if err != nil {
			return fmt.Errorf("строка %d: %v", n, err)
		}
		if bk.stats == nil {
			bk.stats = map[bookKey]Stat{}
		}
		st := bk.stats[bookKey{h, m}]
		switch f[2][0] {
		case 'W':
			st.W++
		case 'L':
			st.L++
		default:
			st.D++
		}
		bk.stats[bookKey{h, m}] = st
	}
	return sc.Err()
}

// SetStatsFile задаёт файл, в который AddResult дописывает результаты.
func (bk *Book) SetStatsFile(path string) { bk.statsFile = path }

func NewBook() *Book { return &Book{m: map[uint64][]BookMove{}} }

// MirrorMove отражает ход слева направо.
func MirrorMove(m board.Move) board.Move {
	if m.Kind() == board.KindPawn {
		i := m.Idx()
		return board.PawnMove(i/9*9 + 8 - i%9)
	}
	i := m.Idx()
	return board.WallMove(m.Kind() == board.KindH, i/8, 7-i%8)
}

// MirrorLine отражает все ходы линии.
func MirrorLine(moves []board.Move) []board.Move {
	out := make([]board.Move, len(moves))
	for i, m := range moves {
		out[i] = MirrorMove(m)
	}
	return out
}

// Positions — сколько различных позиций в книге.
func (bk *Book) Positions() int { return len(bk.m) }

// Lookup — записанные ходы в позиции (nil, если позиции в книге нет).
func (bk *Book) Lookup(b *board.Board) []BookMove { return bk.m[search.Hash(b)] }

// AddLine проигрывает линию с начала партии, проверяя легальность, и записывает ходы стороны side
// (-1 = обе). maxPlies > 0 ограничивает длину линии.
func (bk *Book) AddLine(moves []board.Move, side, maxPlies int) error {
	return bk.addLine(moves, side, maxPlies, !bk.NoMirror)
}

func (bk *Book) addLine(moves []board.Move, side, maxPlies int, mirror bool) error {
	if maxPlies > 0 && len(moves) > maxPlies {
		moves = moves[:maxPlies]
	}
	if err := bk.addOne(moves, side, nil); err != nil {
		return err
	}
	if mirror {
		// Отражённая линия записывается в книгу тоже, но пока она совпадает с исходной (симметричное начало),
		// эти ходы второй раз не считаются.
		if err := bk.addOne(MirrorLine(moves), side, moves); err != nil {
			return err
		}
	}
	bk.Lines++
	return nil
}

func (bk *Book) addOne(moves []board.Move, side int, ref []board.Move) error {
	b := board.New()
	samePrefix := ref != nil
	for i, mv := range moves {
		if !legalMove(&b, mv) {
			return fmt.Errorf("ход %d (%s) нелегален", i+1, FormatMove(mv))
		}
		dup := samePrefix && ref[i] == mv
		if !dup {
			samePrefix = false
		}
		if !dup && (side < 0 || int(b.Turn) == side) {
			k := search.Hash(&b)
			found := false
			for j := range bk.m[k] {
				if bk.m[k][j].Move == mv {
					bk.m[k][j].Weight++
					found = true
				}
			}
			if !found {
				bk.m[k] = append(bk.m[k], BookMove{mv, 1})
			}
		}
		b = b.Apply(mv)
	}
	return nil
}

func legalMove(b *board.Board, m board.Move) bool {
	if m.Kind() == board.KindPawn {
		var buf [8]uint8
		for _, to := range b.PawnMoves(int(b.Turn), buf[:0]) {
			if int(to) == m.Idx() {
				return true
			}
		}
		return false
	}
	if b.Walls[b.Turn] == 0 {
		return false
	}
	return movegen.LegalWall(b, m.Kind() == board.KindH, m.Idx()/8, m.Idx()%8)
}

// ParseBookLine разбирает строку книги. ok=false для пустых строк и комментариев.
func ParseBookLine(line string) (side int, moves []board.Move, ok bool, err error) {
	side, moves, _, ok, err = parseBookLine(line)
	return
}

// parseBookLine дополнительно возвращает nomirror: токен "nomirror" в начале строки отключает отражение линии.
func parseBookLine(line string) (side int, moves []board.Move, nomirror, ok bool, err error) {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	f := strings.Fields(line)
	if len(f) == 0 {
		return 0, nil, false, false, nil
	}
	side = -1
	if strings.HasPrefix(f[0], "side=") {
		switch v := f[0][5:]; v {
		case "*":
		case "0", "1":
			side, _ = strconv.Atoi(v)
		default:
			return 0, nil, false, false, fmt.Errorf("side=%q: нужно 0, 1 или *", v)
		}
		f = f[1:]
	}
	if len(f) > 0 && f[0] == "nomirror" {
		nomirror = true
		f = f[1:]
	}
	for _, s := range f {
		m, e := ParseMove(s)
		if e != nil {
			return 0, nil, false, false, e
		}
		moves = append(moves, m)
	}
	return side, moves, nomirror, len(moves) > 0, nil
}

// FormatBookLine — строка книги для линии.
func FormatBookLine(side int, moves []board.Move) string {
	var sb strings.Builder
	if side < 0 {
		sb.WriteString("side=*")
	} else {
		fmt.Fprintf(&sb, "side=%d", side)
	}
	for _, m := range moves {
		sb.WriteByte(' ')
		sb.WriteString(FormatMove(m))
	}
	return sb.String()
}

// ReadBook читает книгу из файла линий. Ошибка указывает номер строки.
func ReadBook(r io.Reader) (*Book, error) {
	bk, _, err := ReadBookExcluding(r, nil)
	return bk, err
}

// ReadBookExcluding читает книгу, пропуская линии из списка плохих дебютов (см. BadList); возвращает,
// сколько линий пропущено. Файл книги при этом не меняется.
func ReadBookExcluding(r io.Reader, bad *BadList) (*Book, int, error) {
	bk := NewBook()
	skipped := 0
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for n := 1; sc.Scan(); n++ {
		side, moves, nomirror, ok, err := parseBookLine(sc.Text())
		if err != nil {
			return nil, 0, fmt.Errorf("строка %d: %v", n, err)
		}
		if !ok {
			continue
		}
		if side >= 0 && bad.Bad(side, moves) {
			skipped++
			continue
		}
		if err := bk.addLine(moves, side, 0, !nomirror); err != nil {
			return nil, 0, fmt.Errorf("строка %d: %v", n, err)
		}
	}
	return bk, skipped, sc.Err()
}

// BookBot играет по книге, пока позиция в ней есть, иначе отдаёт ход Inner.
// Из нескольких записанных ходов выбирает случайный пропорционально весу, умноженному на накопленные
// результаты (ход, который уже несколько раз проигрывал, отключается).
//
// Плохие дебюты отсеиваются заранее (список плохих дебютов, см. BadList и cmd/book -validate), а не в игре.
// Необязательная проверка в игре (Verify): каждый ход книги проверяется неглубоким поиском, и если его оценка хуже
// лучшего хода поиска больше чем на Margin (в сотых долях шага) или он ведёт к проигранному мату, книга его не играет.
// По умолчанию она выключена: она тратит время на каждом ходу из книги.
type BookBot struct {
	Book        *Book
	Inner       Bot
	Verify      bool // проверять ходы книги поиском в игре (по умолчанию нет)
	Margin      int  // допустимое отставание оценки хода книги от лучшего хода поиска (по умолчанию 150)
	VerifyDepth int  // глубина проверки (по умолчанию 4)
	FromBook    bool // последний ход взят из книги
	Rejected    bool // книга предлагала ходы, но проверка их отвергла
	rng         *rand.Rand
	used        []usedMove
}

type usedMove struct {
	h    uint64
	m    board.Move
	side int
}

var bookSeq atomic.Int64

func NewBookBot(bk *Book, inner Bot) *BookBot {
	return &BookBot{Book: bk, Inner: inner, Margin: 150, VerifyDepth: 4,
		rng: rand.New(rand.NewSource(time.Now().UnixNano() + bookSeq.Add(1)*7919))}
}

func (bb *BookBot) SetHistory(keys []uint64) {
	if h, ok := bb.Inner.(historyAware); ok {
		h.SetHistory(keys)
	}
}

// verified оставляет ходы книги, которые поиск не считает заметно хуже лучшего.
func (bb *BookBot) verified(b board.Board, cand []BookMove) []BookMove {
	s := SearcherOf(bb.Inner)
	if !bb.Verify || s == nil {
		return cand
	}
	s.StopPonder() // поисковик нужен нам: «думалка» не должна работать параллельно
	d := bb.VerifyDepth
	if d < 2 {
		d = 4
	}
	best := s.S.Search(b, search.Limits{MaxDepth: d})
	var ok []BookMove
	for _, bm := range cand {
		nb := b.Apply(bm.Move)
		if nb.Winner() >= 0 {
			ok = append(ok, bm)
			continue
		}
		sc := -s.S.Search(nb, search.Limits{MaxDepth: d - 1}).Score
		if sc < -search.MateThr || sc < best.Score-bb.Margin {
			continue
		}
		ok = append(ok, bm)
	}
	return ok
}

func (bb *BookBot) Choose(b board.Board) board.Move {
	h := search.Hash(&b)
	var cand []BookMove
	for _, bm := range bb.Book.Lookup(&b) {
		if !legalMove(&b, bm.Move) {
			continue
		}
		if bb.Book.stat(h, bm.Move).banned() {
			continue
		}
		cand = append(cand, bm)
	}
	bb.FromBook, bb.Rejected = false, false
	if len(cand) == 0 {
		return bb.Inner.Choose(b)
	}
	if v := bb.verified(b, cand); len(v) < len(cand) {
		cand = v
		if len(cand) == 0 {
			bb.Rejected = true
			return bb.Inner.Choose(b)
		}
	}
	total := 0.0
	w := make([]float64, len(cand))
	for i, bm := range cand {
		w[i] = float64(bm.Weight) * bb.Book.stat(h, bm.Move).factor()
		total += w[i]
	}
	x := bb.rng.Float64() * total
	pick := cand[len(cand)-1].Move
	for i, bm := range cand {
		if x < w[i] {
			pick = bm.Move
			break
		}
		x -= w[i]
	}
	bb.FromBook = true
	bb.used = append(bb.used, usedMove{h, pick, int(b.Turn)})
	return pick
}

// Finish сообщает исход партии (winner: 0/1, -1 = ничья или не закончена): ходы, сыгранные из книги, получают
// результат. Вызывайте один раз после партии; повторный вызов ничего не делает.
func (bb *BookBot) Finish(winner int) {
	for _, u := range bb.used {
		res := byte('D')
		if winner >= 0 {
			res = 'L'
			if winner == u.side {
				res = 'W'
			}
		}
		bb.Book.AddResult(u.h, u.m, res)
	}
	bb.used = nil
}

// StopPonder останавливает «думалку» вложенного поисковика (см. Searcher.StopPonder).
func (bb *BookBot) StopPonder() {
	if s := SearcherOf(bb.Inner); s != nil {
		s.StopPonder()
	}
}

// SearcherOf достаёт поисковик из бота (в том числе обёрнутого книгой) — для показа оценки и глубины.
func SearcherOf(b Bot) *Searcher {
	switch v := b.(type) {
	case *Searcher:
		return v
	case *BookBot:
		return SearcherOf(v.Inner)
	}
	return nil
}

// StartsWithAdvance: первые n ходов стороны side — ходы пешкой, и каждый приближает её к цели
// (синие идут вниз, к строке 8; красные вверх, к строке 0). n <= 0 — условие выключено.
// Прыжки через соперника засчитываются, ходы в сторону и стены — нет.
func StartsWithAdvance(moves []board.Move, side, n int) bool {
	if n <= 0 {
		return true
	}
	row := 0
	if side == 1 {
		row = 8
	}
	own := 0
	for i := side; i < len(moves) && own < n; i += 2 {
		m := moves[i]
		if m.Kind() != board.KindPawn {
			return false
		}
		r := m.Idx() / 9
		if (side == 0 && r <= row) || (side == 1 && r >= row) {
			return false
		}
		row = r
		own++
	}
	return own == n
}

