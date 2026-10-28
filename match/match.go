// Package match: арена бот-против-бота. Игры парные: одна и та же случайная
// дебютная расстановка играется дважды со сменой цвета, чтобы убрать шум дебюта.
package match

import (
	"math/rand"
	"sync"
	"time"

	"quoridor/board"
	"quoridor/endgame"
	"quoridor/movegen"
	"quoridor/path"
	"quoridor/search"
	"quoridor/threat"
)

type Bot interface {
	Choose(b board.Board) board.Move
}

// Walker: «иди по кратчайшему пути», стен не ставит. Базовая планка силы.
type Walker struct{}

func (Walker) Choose(b board.Board) board.Move {
	me := int(b.Turn)
	d := path.Field(&b, board.GoalRow(me))
	var buf [8]uint8
	best, bd := uint8(0), int8(127)
	for _, to := range b.PawnMoves(me, buf[:0]) {
		if d[to] < bd {
			best, bd = to, d[to]
		}
	}
	return board.PawnMove(int(best))
}

// Searcher: бот на поиске с фиксированной глубиной и/или временем.
//
// Если Ponder включён, после своего хода бот не простаивает: он угадывает ответ соперника (по таблице
// транспозиций) и ищет в получившейся позиции, пока не позовут Choose снова или не истечёт PonderMax.
// Если соперник сыграл угаданный ход, поиск стартует с уже прогретой таблицей и доходит глубже за то же время.
type Searcher struct {
	S    *search.Searcher
	Lim  search.Limits
	Last search.Result // итог последнего поиска (оценка, глубина, узлы) — для интерфейсов и отладки

	Ponder       bool          // думать на ходу соперника
	PonderMax    time.Duration // максимум одной «думалки» (0 = 20 с)
	PonderStarts int           // сколько раз запускали «думалку»
	PonderHits   int           // сколько раз соперник сыграл угаданный ход
	pondDone     chan struct{}
	pondPos      board.Board // позиция, в которой думаем (после угаданного ответа)
	pondOn       bool
}

func NewSearcher(ttBits uint, lim search.Limits) *Searcher {
	return &Searcher{S: search.New(ttBits), Lim: lim}
}

// SetHistory передаёт поиску позиции партии (см. search.Searcher.SetHistory).
// «Думалку» приходится остановить: она читает ту же историю.
func (s *Searcher) SetHistory(keys []uint64) {
	s.StopPonder()
	s.S.SetHistory(keys)
}

func (s *Searcher) Choose(b board.Board) board.Move {
	hit := s.pondOn && b == s.pondPos
	s.StopPonder()
	s.pondOn = false
	if hit {
		s.PonderHits++
	}
	s.S.ResetAbort()
	s.Last = s.S.Search(b, s.Lim)
	if s.Ponder {
		s.startPonder(b, s.Last.Move)
	}
	return s.Last.Move
}

// StopPonder останавливает «думалку» и дожидается её конца (после этого поисковиком снова можно пользоваться).
// Безопасно вызывать, когда она не запущена. Вызывайте, когда партия закончилась или началась новая.
func (s *Searcher) StopPonder() {
	if s.pondDone != nil {
		s.S.Abort()
		<-s.pondDone
		s.pondDone = nil
	}
	s.S.ResetAbort()
}

func (s *Searcher) startPonder(b board.Board, m board.Move) {
	after := b.Apply(m)
	if after.Winner() >= 0 {
		return
	}
	pred, ok := s.S.TTMove(&after)
	if !ok || !legalMove(&after, pred) {
		return
	}
	next := after.Apply(pred)
	if next.Winner() >= 0 || endgame.Solvable(&next) {
		return
	}
	max := s.PonderMax
	if max <= 0 {
		max = 20 * time.Second
	}
	s.pondPos, s.pondOn = next, true
	s.PonderStarts++
	done := make(chan struct{})
	s.pondDone = done
	s.S.ResetAbort()
	go func() {
		defer close(done)
		s.S.Search(next, search.Limits{Time: max})
	}()
}

type Record struct {
	Moves  []board.Move
	Winner int // 0, 1 или -1 (ничья по лимиту ходов)
	ASide  int // какой стороной играл бот A (0/1), -1 если неизвестно
}

// historyAware: бот, которому можно сообщить позиции партии (повторы = ничья).
type historyAware interface{ SetHistory(keys []uint64) }

// PlayGame играет партию: p0 ходит первым. opening играется заранее без участия ботов.
// Партия заканчивается ничьей при троекратном повторении позиции или по лимиту maxPlies.
func PlayGame(p0, p1 Bot, opening []board.Move, maxPlies int) Record {
	b := board.New()
	rec := Record{Winner: -1, ASide: -1}
	var keys []uint64 // ключи всех позиций до текущей
	count := map[uint64]int{}
	for _, m := range opening {
		keys = append(keys, search.Hash(&b))
		count[keys[len(keys)-1]]++
		b = b.Apply(m)
		rec.Moves = append(rec.Moves, m)
	}
	bots := [2]Bot{p0, p1}
	for len(rec.Moves) < maxPlies {
		if w := b.Winner(); w >= 0 {
			rec.Winner = w
			return rec
		}
		k := search.Hash(&b)
		if count[k] >= 2 { // текущая позиция в третий раз
			return rec
		}
		bot := bots[b.Turn]
		if h, ok := bot.(historyAware); ok {
			h.SetHistory(keys)
		}
		keys = append(keys, k)
		count[k]++
		m := bot.Choose(b)
		b = b.Apply(m)
		rec.Moves = append(rec.Moves, m)
	}
	if w := b.Winner(); w >= 0 {
		rec.Winner = w
	}
	return rec
}

// RandomOpening: n случайных легальных полуходов.
func RandomOpening(rng *rand.Rand, n int) []board.Move {
	b := board.New()
	var out []board.Move
	for i := 0; i < n; i++ {
		ms := movegen.Gen(&b, nil)
		m := ms[rng.Intn(len(ms))]
		out = append(out, m)
		b = b.Apply(m)
	}
	return out
}

type Score struct {
	AWins, BWins, Draws int
	Games               []Record // с точки зрения порядка (Aпервый, Aвторой) по парам
	Elapsed             time.Duration
}

func (s Score) Total() int { return s.AWins + s.BWins + s.Draws }

// Rate: доля очков A (ничья = 0.5).
func (s Score) Rate() float64 {
	if s.Total() == 0 {
		return 0
	}
	return (float64(s.AWins) + 0.5*float64(s.Draws)) / float64(s.Total())
}

// Run играет pairs пар партий (2*pairs партий). newA/newB создают свежих ботов на каждую партию
// (у ботов есть состояние вроде TT, а игры идут параллельно).
func Run(newA, newB func() Bot, pairs, openingPlies, maxPlies, workers int, seed int64) Score {
	start := time.Now()
	type job struct{ pair int }
	jobs := make(chan int)
	var mu sync.Mutex
	var sc Score
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				rng := rand.New(rand.NewSource(seed + int64(i)))
				op := RandomOpening(rng, openingPlies)
				// A играет первой, затем B играет первой с тем же дебютом
				r1 := PlayGame(newA(), newB(), op, maxPlies)
				r2 := PlayGame(newB(), newA(), op, maxPlies)
				r1.ASide, r2.ASide = 0, 1
				mu.Lock()
				for k, r := range []Record{r1, r2} {
					aSide := k // в партии 0 A — игрок 0, в партии 1 A — игрок 1
					switch {
					case r.Winner < 0:
						sc.Draws++
					case r.Winner == aSide:
						sc.AWins++
					default:
						sc.BWins++
					}
					sc.Games = append(sc.Games, r)
				}
				mu.Unlock()
			}
		}()
	}
	for i := 0; i < pairs; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	sc.Elapsed = time.Since(start)
	return sc
}

// ThreatBot: перед обычным поиском ищет форсированную ловушку стенами (threat.Find) и играет её первую стену.
type ThreatBot struct {
	Base  Bot
	Depth int // моих стен в ловушке (обычно 2)
	Thr   int // требуемый выигрыш пути сверх ходьбы
}

func (t *ThreatBot) Choose(b board.Board) board.Move {
	if b.Walls[b.Turn] > 0 {
		if r := threat.Find(&b, t.Depth, t.Thr); r.Found {
			return r.Move
		}
	}
	return t.Base.Choose(b)
}

