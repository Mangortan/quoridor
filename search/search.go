// Package search: negamax + alpha-beta + итеративное углубление + таблица транспозиций
// + упорядочивание ходов (ход из TT, продвижение пешки, killer, history).
//
// В горячем цикле нет аллокаций: все буферы лежат в Searcher и создаются один раз в New.
package search

import (
	"math/bits"
	"sync/atomic"
	"time"

	"quoridor/board"
	"quoridor/endgame"
	"quoridor/eval"
	"quoridor/movegen"
	"quoridor/path"
)

const (
	Mate     = 30000
	MateThr  = Mate - 1000
	Inf      = Mate + 1
	maxPly   = 64
	maxMv    = 160
	histSize = 2048 // степень двойки; не более histSize/2 ключей
)

const (
	flagExact = iota
	flagLower // score >= beta (отсечение)
	flagUpper // score <= alpha
)

// slot — запись таблицы для нескольких потоков без блокировок: данные пакуются в одно слово,
// а ключ хранится как key^data. Порванная (записанная двумя потоками пополам) запись не совпадёт
// по ключу и просто будет проигнорирована.
type slot struct { // 16 байт
	k uint64
	d uint64
}

type entry struct {
	score int16
	depth int8
	flag  uint8
	move  uint16
}

func (s *Searcher) probe(key uint64) (e entry, ok bool) {
	sl := &s.tt[key&s.mask]
	d := atomic.LoadUint64(&sl.d)
	if atomic.LoadUint64(&sl.k)^d != key {
		return entry{}, false
	}
	return entry{score: int16(d), depth: int8(d >> 16), flag: uint8(d >> 24), move: uint16(d >> 32)}, true
}

func (s *Searcher) store(key uint64, e entry) {
	d := uint64(uint16(e.score)) | uint64(uint8(e.depth))<<16 | uint64(e.flag)<<24 | uint64(e.move)<<32
	sl := &s.tt[key&s.mask]
	atomic.StoreUint64(&sl.d, d)
	atomic.StoreUint64(&sl.k, key^d)
}

type Limits struct {
	MaxDepth int           // 0 = без ограничения (до maxPly-4)
	Time     time.Duration // 0 = без ограничения
}

type Result struct {
	Move    board.Move
	Score   int
	Depth   int // последняя полностью завершённая глубина
	Nodes   uint64
	Elapsed time.Duration
}

type Searcher struct {
	// Eval принимает позицию по значению: так указатель не «утекает» в вызов по значению-функции
	// и позиции в рекурсии остаются на стеке (иначе аллокация на каждый узел).
	Eval func(board.Board) int

	// ExtendWalls: продлевать ветку на 1 ход после стены, удлиняющей путь соперника на >=2
	// (не более ExtMax продлений на одной ветке; 0 = 2).
	// NoEndgame отключает точное решение позиций без стен (см. пакет endgame); по умолчанию оно включено.
	NoEndgame bool
	// NoRace отключает правило «у кого нет стен, тот идёт по кратчайшему пути вместо хождения туда-сюда».
	NoRace bool

	ExtendWalls bool
	ExtMax      int8

	// PVS: первый ход с полным окном, остальные с нулевым окном (перепоиск при превышении alpha).
	PVS bool
	// LMR: поздние стены (после LMRFrom просмотренных ходов, не killer/TT) ищутся на 1 ход мельче;
	// если результат превысил alpha — перепоиск на полной глубине. Включает PVS.
	LMR     bool
	LMRFrom int // 0 = 3

	// TM: управление временем. Limits.Time считается целевым: при устойчивом ходе поиск заканчивается
	// раньше, при смене хода или падении оценки получает до 2.2 целевого времени.
	TM bool

	// WallSlack: расширять набор стен-кандидатов рёбрами обходных путей (длина <= кратчайший+WallSlack). 0 = только кратчайшие.
	WallSlack int
	// WallConn: добавлять в кандидаты все стены, касающиеся края доски/других стен не менее чем WallConn точками (0 = нет).
	WallConn int
	// ConnReduce: «достраивающие» стены (WallConn) ищутся на 1 ход мельче основных, с перепоиском при успехе.
	ConnReduce bool

	// Threads: число потоков поиска (Lazy SMP: все потоки ищут одну позицию с общей таблицей
	// транспозиций, обмениваясь только ею). 0 или 1 = один поток.
	Threads int

	helpers []*Searcher
	startD  int // с какой глубины начинает итерации поток-помощник

	tt   []slot
	mask uint64

	moves   [maxPly][maxMv]board.Move
	extra   [maxPly][maxMv]bool
	scores  [maxPly][maxMv]int32
	keys    [maxPly + 1]uint64
	extUsed [maxPly + 1]int8
	hist    [histSize]uint64 // позиции уже сыгранной партии (открытая адресация): повтор = ничья
	nHist   int
	boards  [maxPly + 1]board.Board // позиции по глубине: так в рекурсии нет адресов локальных переменных
	killers [maxPly][2]board.Move
	history [2][576]int32

	egCache map[[2]uint64]*endgame.Table // решённые эндшпили по расстановке стен

	abort atomic.Bool // см. Abort: прерывает идущий поиск из другой горутины (нужно для «думать на чужом ходу»)

	nodes     uint64
	stop      bool
	deadline  time.Time
	rootBest  board.Move
	rootScore int
	rootDone  bool
}

// New создаёт поисковик с TT на 2^ttBits записей (16 байт каждая; 20 -> 16 МБ).
func New(ttBits uint) *Searcher {
	return &Searcher{
		Eval: func(b board.Board) int { return eval.V1(&b) },
		tt:   make([]slot, 1<<ttBits),
		mask: 1<<ttBits - 1,
	}
}

// Clear: новая партия.
func (s *Searcher) Clear() {
	for i := range s.tt {
		s.tt[i] = slot{}
	}
	s.killers = [maxPly][2]board.Move{}
	s.history = [2][576]int32{}
}

func mix(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return x
}

// Hash: ключ позиции (стены, пешки, запас стен, чей ход).
func Hash(b *board.Board) uint64 {
	pack := uint64(b.P[0]) | uint64(b.P[1])<<8 | uint64(b.Walls[0])<<16 | uint64(b.Walls[1])<<24 | uint64(b.Turn)<<32
	return mix(b.HW*0x9E3779B97F4A7C15 ^ bits.RotateLeft64(b.VW*0xC2B2AE3D27D4EB4F, 23) ^ mix(pack+0x632BE59BD9B4E019))
}

// SetHistory сообщает поиску позиции, уже встречавшиеся в партии (ключи Hash, без текущей).
// Повторение такой позиции в поиске оценивается как ничья (0), поэтому бот, у которого оценка
// положительна, не будет бесконечно ходить по кругу, а тот, кто проигрывает, может согласиться на повтор.
func (s *Searcher) SetHistory(keys []uint64) {
	s.hist = [histSize]uint64{}
	s.nHist = 0
	for _, k := range keys {
		if s.nHist >= histSize/2 {
			break
		}
		if k == 0 {
			k = 1
		}
		i := int(k) & (histSize - 1)
		for s.hist[i] != 0 && s.hist[i] != k {
			i = (i + 1) & (histSize - 1)
		}
		if s.hist[i] == 0 {
			s.hist[i] = k
			s.nHist++
		}
	}
}

func (s *Searcher) inHistory(key uint64) bool {
	if s.nHist == 0 {
		return false
	}
	if key == 0 {
		key = 1
	}
	for i := int(key) & (histSize - 1); s.hist[i] != 0; i = (i + 1) & (histSize - 1) {
		if s.hist[i] == key {
			return true
		}
	}
	return false
}

// Search ищет лучший ход. Не аллоцирует.
func (s *Searcher) Search(b board.Board, lim Limits) Result {
	if !s.NoEndgame && endgame.Solvable(&b) {
		return s.solveEndgame(&b)
	}
	wait := s.startHelpers(&b, lim)
	res := s.iterate(b, lim)
	if wait != nil {
		res.Nodes += wait()
	}
	if !s.NoRace && b.Walls[b.Turn] == 0 && b.Winner() < 0 {
		res.Move = s.raceMove(&b, res)
	}
	return res
}

// raceMove: у ходящего нет стен, и ему остаётся только идти к цели. Если поиск выбрал ход, который
// не приближает пешку к цели или повторяет позицию партии (боты в этом случае мечутся туда-сюда до ничьей),
// идём по кратчайшему пути. Найденный мат (в любую сторону) не трогаем.
func (s *Searcher) raceMove(b *board.Board, res Result) board.Move {
	if res.Score > MateThr || res.Score < -MateThr {
		return res.Move
	}
	turn := int(b.Turn)
	cur := path.Len(b, turn)
	if res.Move.Kind() == board.KindPawn {
		nb := b.Apply(res.Move)
		if nd := path.Len(&nb, turn); nd >= 0 && nd < cur && !s.inHistory(Hash(&nb)) {
			return res.Move
		}
	}
	var buf [8]uint8
	best, bestD := res.Move, cur
	for _, to := range b.PawnMoves(turn, buf[:0]) {
		m := board.PawnMove(int(to))
		nb := b.Apply(m)
		nd := path.Len(&nb, turn)
		if nd < 0 || nd >= bestD || s.inHistory(Hash(&nb)) {
			continue
		}
		best, bestD = m, nd
	}
	return best
}

func (s *Searcher) iterate(b board.Board, lim Limits) Result {
	start := time.Now()
	s.nodes, s.stop = 0, false
	soft := lim.Time
	if lim.Time > 0 {
		hard := lim.Time
		if s.TM {
			hard = lim.Time * 16 / 10
		}
		s.deadline = start.Add(hard)
	} else {
		s.deadline = start.Add(24 * time.Hour)
	}
	maxD := maxPly - 4
	if lim.MaxDepth > 0 && lim.MaxDepth < maxD {
		maxD = lim.MaxDepth
	}
	var res Result
	var prevMove board.Move
	prevScore, stable := 0, 0
	d0 := 1
	if s.startD > 1 {
		d0 = s.startD
	}
	for d := d0; d <= maxD; d++ {
		s.rootDone = false
		s.boards[0] = b
		s.extUsed[0] = 0
		score := s.negamax(&s.boards[0], d, -Inf, Inf, 0)
		if s.stop {
			if s.rootDone { // ход, полностью проверенный на этой глубине, годится
				res.Move, res.Score = s.rootBest, s.rootScore
			}
			break
		}
		res.Move, res.Score, res.Depth = s.rootBest, score, d
		if score > MateThr || score < -MateThr {
			break
		}
		if s.TM && soft > 0 {
			if d > 1 && res.Move == prevMove && score > prevScore-25 {
				stable++
			} else {
				stable = 0
			}
			prevMove, prevScore = res.Move, score
			el := time.Since(start)
			// Следующая глубина стоит примерно в 2.5 раза больше текущей. Начинаем её, только если
			// она успеет закончиться в отведённое время: T при устойчивом ходе, 1.6 T при нестабильном.
			limit := soft
			if stable == 0 {
				limit = soft * 16 / 10
			}
			if d >= 3 && stable >= 2 && el*5 >= soft*2 {
				return s.finish(res, start) // ход устойчив уже три глубины подряд
			}
			if el*5/2 > limit {
				return s.finish(res, start)
			}
		}
	}
	return s.finish(res, start)
}

func (s *Searcher) finish(res Result, start time.Time) Result {
	res.Nodes, res.Elapsed = s.nodes, time.Since(start)
	return res
}

func (s *Searcher) negamax(b *board.Board, depth, alpha, beta, ply int) int {
	s.nodes++
	if s.nodes&1023 == 0 && (s.abort.Load() || time.Now().After(s.deadline)) {
		s.stop = true
	}
	if s.stop {
		return 0
	}
	if b.Winner() >= 0 { // победитель — тот, кто только что походил
		return -(Mate - ply)
	}
	key := Hash(b)
	if ply > 0 {
		for i := ply - 2; i >= 0; i -= 2 {
			if s.keys[i] == key {
				return 0 // повторение позиции на пути: ничья
			}
		}
		if s.inHistory(key) {
			return 0 // позиция уже была в партии: ничья
		}
	}
	s.keys[ply] = key
	if depth <= 0 || ply >= maxPly-2 {
		return s.Eval(*b)
	}

	alpha0 := alpha
	var ttMove board.Move
	if e, ok := s.probe(key); ok {
		ttMove = board.Move(e.move)
		if int(e.depth) >= depth && ply > 0 {
			sc := int(e.score)
			if sc > MateThr {
				sc -= ply
			} else if sc < -MateThr {
				sc += ply
			}
			switch e.flag {
			case flagExact:
				return sc
			case flagLower:
				if sc > alpha {
					alpha = sc
				}
			case flagUpper:
				if sc < beta {
					beta = sc
				}
			}
			if alpha >= beta {
				return sc
			}
		}
	}

	var ms []board.Move
	nBase := 0
	if s.ConnReduce && s.WallConn > 0 {
		ms, nBase = movegen.CandidatesSplit(b, s.moves[ply][:0], s.WallConn)
	} else {
		ms = movegen.CandidatesOpt(b, s.moves[ply][:0], s.WallSlack, s.WallConn)
		nBase = len(ms)
	}
	xf := s.extra[ply][:len(ms)]
	for i := range xf {
		xf[i] = i >= nBase
	}
	n := len(ms)
	sc := s.scores[ply][:n]
	turn := int(b.Turn)
	var d [81]int8
	haveD := depth >= 2
	if haveD {
		d = path.Field(b, board.GoalRow(turn))
	}
	cur := int(b.P[turn])
	for i, m := range ms {
		var v int32
		switch {
		case m == ttMove:
			v = 1 << 30
		case m == s.killers[ply][0]:
			v = 1 << 21
		case m == s.killers[ply][1]:
			v = 1<<21 - 1
		case m.Kind() == board.KindPawn && haveD:
			v = 1<<22 + 100*int32(d[cur]-d[m.Idx()])
		default:
			v = s.history[turn][m]
		}
		sc[i] = v
	}

	best := -Inf
	var bestMove board.Move
	tried := 0
	dOpp0 := -1
	for i := 0; i < n; i++ {
		bi := i // выбор лучшего по оценке
		for j := i + 1; j < n; j++ {
			if sc[j] > sc[bi] {
				bi = j
			}
		}
		ms[i], ms[bi] = ms[bi], ms[i]
		xf[i], xf[bi] = xf[bi], xf[i]
		sc[i], sc[bi] = sc[bi], sc[i]
		m := ms[i]
		if !movegen.Legal(b, m) {
			continue
		}
		s.boards[ply+1] = b.Apply(m)
		nd := depth - 1
		s.extUsed[ply+1] = s.extUsed[ply]
		if s.ExtendWalls && depth >= 2 && m.Kind() != board.KindPawn {
			lim := s.ExtMax
			if lim == 0 {
				lim = 2
			}
			if s.extUsed[ply] < lim {
				if dOpp0 < 0 {
					dOpp0 = path.Len(b, 1-turn)
				}
				if path.Len(&s.boards[ply+1], 1-turn)-dOpp0 >= 2 {
					nd++
					s.extUsed[ply+1]++
				}
			}
		}
		var score int
		if xf[i] && nd >= 2 && sc[i] < 1<<21-1 { // «достраивающая» стена: сначала мельче, перепоиск если она хороша
			score = -s.negamax(&s.boards[ply+1], nd-1, -beta, -alpha, ply+1)
			if !s.stop && score > alpha {
				score = -s.negamax(&s.boards[ply+1], nd, -beta, -alpha, ply+1)
			}
		} else if (s.PVS || s.LMR) && tried > 0 {
			red := 0
			from := s.LMRFrom
			if from == 0 {
				from = 3
			}
			if s.LMR && tried >= from && nd >= 3 && m.Kind() != board.KindPawn && sc[i] < 1<<21-1 && alpha > -MateThr && alpha < MateThr {
				red = 1
			}
			score = -s.negamax(&s.boards[ply+1], nd-red, -alpha-1, -alpha, ply+1)
			if !s.stop && score > alpha && (red > 0 || score < beta) {
				score = -s.negamax(&s.boards[ply+1], nd, -beta, -alpha, ply+1)
			}
		} else {
			score = -s.negamax(&s.boards[ply+1], nd, -beta, -alpha, ply+1)
		}
		if s.stop {
			return 0
		}
		tried++
		if score > best {
			best, bestMove = score, m
			if ply == 0 {
				s.rootBest, s.rootScore, s.rootDone = m, score, true
			}
			if score > alpha {
				alpha = score
				if alpha >= beta {
					if s.killers[ply][0] != m {
						s.killers[ply][1], s.killers[ply][0] = s.killers[ply][0], m
					}
					s.history[turn][m] += int32(depth * depth)
					break
				}
			}
		}
	}
	if tried == 0 {
		return s.Eval(*b)
	}

	flag := uint8(flagExact)
	if best <= alpha0 {
		flag = flagUpper
	} else if best >= beta {
		flag = flagLower
	}
	st := best
	if st > MateThr {
		st += ply
	} else if st < -MateThr {
		st -= ply
	}
	s.store(key, entry{score: int16(st), depth: int8(depth), flag: flag, move: uint16(bestMove)})
	return best
}

// solveEndgame: у обоих нет стен — точный выбор хода (кратчайший выигрыш, иначе кратчайший путь к цели).
func (s *Searcher) solveEndgame(b *board.Board) Result {
	start := time.Now()
	if s.egCache == nil || len(s.egCache) > 8 {
		s.egCache = map[[2]uint64]*endgame.Table{}
	}
	key := [2]uint64{b.HW, b.VW}
	t := s.egCache[key]
	if t == nil {
		t = endgame.Solve(b)
		s.egCache[key] = t
	}
	m, res, plies := t.Best(b, (*egSeen)(s))
	r := Result{Move: m, Depth: plies, Nodes: 1, Elapsed: time.Since(start)}
	switch res {
	case 1:
		r.Score = Mate - plies
	case -1:
		r.Score = -(Mate - plies)
	}
	return r
}

// egSeen: адаптер для endgame.Avoider без аллокаций (позиции, уже бывшие в партии).
type egSeen Searcher

func (e *egSeen) Seen(next board.Board) bool { return (*Searcher)(e).inHistory(Hash(&next)) }

// Abort просит идущий поиск остановиться (можно вызывать из другой горутины). Поиск завершится в течение
// ~1000 узлов и вернёт результат последней полностью завершённой глубины. Флаг остаётся поднятым, пока не вызван ResetAbort.
func (s *Searcher) Abort() { s.abort.Store(true) }

// ResetAbort снимает флаг Abort; вызывайте перед очередным поиском.
func (s *Searcher) ResetAbort() { s.abort.Store(false) }

// TTMove: лучший ход в позиции b из таблицы транспозиций (то, что поиск считал лучшим ответом).
// Нужен, чтобы угадать ход соперника. Не вызывайте во время идущего поиска.
func (s *Searcher) TTMove(b *board.Board) (board.Move, bool) {
	key := Hash(b)
	e, ok := s.probe(key)
	if !ok || e.move == 0 {
		return 0, false
	}
	return board.Move(e.move), true
}

// startHelpers запускает потоки-помощники (Threads-1 штук) на той же позиции. Возвращает функцию,
// которая останавливает их и отдаёт число их узлов; nil, если поток один.
func (s *Searcher) startHelpers(b *board.Board, lim Limits) func() uint64 {
	n := s.Threads - 1
	if n <= 0 {
		return nil
	}
	for len(s.helpers) < n {
		s.helpers = append(s.helpers, &Searcher{tt: s.tt, mask: s.mask})
	}
	pos := *b // копия: сам b остаётся на стеке вызывающего (иначе лишняя аллокация и в однопоточном поиске)
	done := make(chan struct{}, n)
	hs := s.helpers[:n]
	for i, h := range hs {
		h.Eval, h.ExtendWalls, h.ExtMax = s.Eval, s.ExtendWalls, s.ExtMax
		h.PVS, h.LMR, h.LMRFrom = s.PVS, s.LMR, s.LMRFrom
		h.WallSlack, h.WallConn, h.ConnReduce = s.WallSlack, s.WallConn, s.ConnReduce
		h.hist, h.nHist = s.hist, s.nHist
		h.startD = 1 + i%2 // половина помощников стартует на ход глубже: потоки расходятся по дереву
		h.abort.Store(false)
		go func(h *Searcher) {
			h.iterate(pos, Limits{MaxDepth: lim.MaxDepth, Time: hardTime(s, lim)})
			done <- struct{}{}
		}(h)
	}
	return func() uint64 {
		var nodes uint64
		for _, h := range hs {
			h.abort.Store(true)
		}
		for range hs {
			<-done
		}
		for _, h := range hs {
			nodes += h.nodes
		}
		return nodes
	}
}

// hardTime: помощникам хватает жёсткого предела; остановит их основной поток.
func hardTime(s *Searcher, lim Limits) time.Duration {
	if lim.Time > 0 && s.TM {
		return lim.Time * 16 / 10
	}
	return lim.Time
}

