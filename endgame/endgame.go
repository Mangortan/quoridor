// Package endgame точно решает позиции, в которых у обоих игроков кончились стены.
//
// Тогда играют только пешки по неизменной расстановке стен, и всё состояние — (пешка 0, пешка 1, чей ход):
// не больше 2*81*81 позиций. Решаем ретроградным анализом: для каждой позиции знаем, выигрывает ли
// ходящий, проигрывает или это ничья (игра может ходить по кругу), и за сколько полуходов.
// Прыжки через пешку и блокировка друг друга в коридорах учитываются правилами board.PawnMoves.
package endgame

import (
	"quoridor/board"
	"quoridor/path"
)

const nStates = 2 * 81 * 81

func sidx(t, p0, p1 int) int { return (t*81+p0)*81 + p1 }

// Table: исходы всех позиций для одной расстановки стен. res с точки зрения ходящего: 1 — выигрыш, -1 — проигрыш, 0 — ничья.
type Table struct {
	res [nStates]int8
	dep [nStates]int16 // полуходов до конца при лучшей игре (для выигрыша — кратчайший, для проигрыша — самый долгий)
}

// Solvable: позиция без стен у обоих и ещё не законченная.
func Solvable(b *board.Board) bool {
	return b.Walls[0] == 0 && b.Walls[1] == 0 && b.Winner() < 0
}

// Solve строит таблицу по стенам из b (HW, VW); пешки и очередь хода не важны.
func Solve(b *board.Board) *Table {
	t := &Table{}
	succ := make([][5]int32, nStates)
	nsucc := make([]uint8, nStates)
	predCnt := make([]int32, nStates+1)
	valid := make([]bool, nStates)
	var buf [8]uint8

	for turn := 0; turn < 2; turn++ {
		for p0 := 0; p0 < 81; p0++ {
			for p1 := 0; p1 < 81; p1++ {
				if p0 == p1 {
					continue
				}
				s := sidx(turn, p0, p1)
				valid[s] = true
				nb := board.Board{HW: b.HW, VW: b.VW, P: [2]uint8{uint8(p0), uint8(p1)}, Turn: uint8(turn)}
				if w := nb.Winner(); w >= 0 {
					if w == turn { // недостижимо: ходит тот, кто уже выиграл
						t.res[s] = 1
					} else {
						t.res[s] = -1
					}
					continue
				}
				for _, to := range nb.PawnMoves(turn, buf[:0]) {
					q0, q1 := p0, p1
					if turn == 0 {
						q0 = int(to)
					} else {
						q1 = int(to)
					}
					n := sidx(turn^1, q0, q1)
					succ[s][nsucc[s]] = int32(n)
					nsucc[s]++
					predCnt[n+1]++
				}
			}
		}
	}
	// предшественники в формате CSR
	for i := 0; i < nStates; i++ {
		predCnt[i+1] += predCnt[i]
	}
	preds := make([]int32, predCnt[nStates])
	fill := make([]int32, nStates)
	for s := 0; s < nStates; s++ {
		for k := 0; k < int(nsucc[s]); k++ {
			n := succ[s][k]
			preds[predCnt[n]+fill[n]] = int32(s)
			fill[n]++
		}
	}
	// ретроградный BFS по слоям глубины
	remaining := make([]uint8, nStates)
	copy(remaining, nsucc)
	queue := make([]int32, 0, nStates)
	for s := 0; s < nStates; s++ {
		if valid[s] && t.res[s] == -1 {
			queue = append(queue, int32(s))
		}
	}
	for h := 0; h < len(queue); h++ {
		s := queue[h]
		for _, p := range preds[predCnt[s]:predCnt[s+1]] {
			if t.res[p] != 0 {
				continue
			}
			if t.res[s] == -1 { // из p можно пойти в позицию, проигранную для соперника
				t.res[p], t.dep[p] = 1, t.dep[s]+1
				queue = append(queue, p)
			} else { // t.res[s] == 1
				remaining[p]--
				if remaining[p] == 0 { // все ходы ведут к выигрышу соперника
					t.res[p], t.dep[p] = -1, t.dep[s]+1
					queue = append(queue, p)
				}
			}
		}
	}
	return t
}

// Outcome: исход для ходящего в b и число полуходов (для ничьей 0).
func (t *Table) Outcome(b *board.Board) (res, plies int) {
	s := sidx(int(b.Turn), int(b.P[0]), int(b.P[1]))
	return int(t.res[s]), int(t.dep[s])
}

// Avoider сообщает, что позиция уже встречалась в партии.
type Avoider interface{ Seen(next board.Board) bool }

// Best выбирает ход пешкой в b (позиция без стен у обоих):
//   - есть выигрыш — самый короткий (по числу полуходов);
//   - иначе ничья — ход по кратчайшему пути к цели (не в проигранную позицию);
//   - иначе (проигрыш) — самый долгий.
//
// avoid.Seen(next) = true помечает позиции, которые уже были в партии: их выбираем в последнюю очередь (avoid может быть nil).
// Возвращает ход, исход для ходящего и полуходов до конца.
func (t *Table) Best(b *board.Board, avoid Avoider) (best board.Move, res, plies int) {
	var buf [8]uint8
	turn := int(b.Turn)
	type cand struct {
		m        board.Move
		res, dep int // с точки зрения ходящего ПОСЛЕ хода (то есть соперника)
		dist     int
		seen     bool
	}
	var csBuf [8]cand // не больше 5 ходов пешкой: без аллокаций
	cs := csBuf[:0]
	for _, to := range b.PawnMoves(turn, buf[:0]) {
		m := board.PawnMove(int(to))
		nb := b.Apply(m)
		r, d := t.Outcome(&nb)
		if nb.Winner() >= 0 {
			r, d = -1, 0
		}
		c := cand{m: m, res: r, dep: d, dist: path.Len(&nb, turn)}
		if avoid != nil && nb.Winner() < 0 {
			c.seen = avoid.Seen(nb)
		}
		cs = append(cs, c)
	}
	if len(cs) == 0 {
		return 0, 0, 0
	}
	better := func(a, b cand) bool { // a лучше b для ходящего
		ra, rb := -a.res, -b.res // исход для нас
		if ra != rb {
			return ra > rb
		}
		switch ra {
		case 1: // выигрыш: кратчайший (он заведомо без повторов)
			if a.dep != b.dep {
				return a.dep < b.dep
			}
		case -1: // проигрыш: оттягиваем конец
			if a.dep != b.dep {
				return a.dep > b.dep
			}
		default: // ничья: не повторяем позиции партии и идём кратчайшим путём к цели
			if a.seen != b.seen {
				return !a.seen
			}
		}
		return a.dist < b.dist
	}
	bi := 0
	for i := 1; i < len(cs); i++ {
		if better(cs[i], cs[bi]) {
			bi = i
		}
	}
	c := cs[bi]
	res = -c.res
	if res != 0 {
		plies = c.dep + 1
	}
	return c.m, res, plies
}

