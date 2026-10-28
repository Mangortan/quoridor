// Package threat: узкий поиск «только стены» для форсированных ловушек.
//
// Ищем последовательность: моя стена, ЛЮБОЙ ответ соперника, моя стена, ... (depth моих стен),
// после которой разность путей (путь соперника минус мой) больше, чем при простой ходьбе, на thr и более.
// Это И/ИЛИ-поиск: по моим стенам — «существует», по ответам соперника — «для всех».
//
// Классика: «одна стена отдельно (почти не удлиняет путь), вторая запирает». У соперника нет
// единственного ответа, который снимает обе угрозы сразу.
//
// Мерка выигрыша: gain = (dOpp - dMe) в конце - (dOpp - dMe) в корне - 1.
// Ходьба даёт «ничью» по темпу (я +1 шаг, он +1 шаг), поэтому «+1» вычитается.
//
// Полнота: для depth <= 2 ответы соперника перебираются ВСЕ (найденная ловушка действительно
// форсированная при тех кандидатах, что перебрали мы). Для depth 3 ответы урезаны эвристикой.
package threat

import (
	"quoridor/board"
	"quoridor/movegen"
	"quoridor/path"
)

type Result struct {
	Found bool
	Move  board.Move // первая стена
	Gain  int        // достигнутый gain (>= thr)
	Nodes int
}

type finder struct {
	thr        int
	exhaustive bool // test-only: все легальные стены на всех шагах
	me         int
	d0         int
	nodes      int
}

var dr = [4]int{-1, 1, 0, 0}
var dc = [4]int{0, 0, -1, 1}

func margin(b *board.Board, me int) int { return path.Len(b, 1-me) - path.Len(b, me) }

// Find: depth = число моих стен в последовательности (1..3).
func Find(b *board.Board, depth, thr int) Result {
	f := finder{thr: thr, me: int(b.Turn)}
	return f.run(b, depth)
}

// FindExhaustive: эталон для тестов (медленнее, кандидаты — все легальные стены).
func FindExhaustive(b *board.Board, depth, thr int) Result {
	f := finder{thr: thr, me: int(b.Turn), exhaustive: true}
	return f.run(b, depth)
}

func (f *finder) run(b *board.Board, depth int) Result {
	if b.Winner() >= 0 || b.Walls[b.Turn] == 0 || depth < 1 {
		return Result{}
	}
	f.d0 = margin(b, f.me)
	res := Result{}
	for _, w := range f.wallCands(b, depth == 1) {
		nb := b.Apply(w)
		f.nodes++
		ok := false
		gain := 0
		if depth == 1 {
			gain = margin(&nb, f.me) - f.d0 - 1
			ok = gain >= f.thr
		} else {
			ok = f.and(&nb, depth-1)
			if ok {
				gain = f.thr // нижняя оценка: gain >= thr
			}
		}
		if ok {
			res = Result{Found: true, Move: w, Gain: gain}
			break
		}
	}
	res.Nodes = f.nodes
	return res
}

// and: ход соперника; ловушка верна, если после КАЖДОГО его ответа у меня есть продолжение.
func (f *finder) and(b *board.Board, k int) bool {
	for _, r := range f.replies(b, k) {
		nb := b.Apply(r)
		f.nodes++
		if nb.Winner() >= 0 {
			return false
		}
		if !f.or(&nb, k) {
			return false
		}
	}
	return true
}

// or: мой ход, нужно k стен подряд (с ответами между ними).
func (f *finder) or(b *board.Board, k int) bool {
	if b.Walls[b.Turn] == 0 {
		return false
	}
	for _, w := range f.wallCands(b, k == 1) {
		nb := b.Apply(w)
		f.nodes++
		if k == 1 {
			if margin(&nb, f.me)-f.d0-1 >= f.thr {
				return true
			}
		} else if f.and(&nb, k-1) {
			return true
		}
	}
	return false
}

// replies: ответы соперника: все ходы пешкой и стены (полный перебор при k<=1, т.е. для depth<=2).
func (f *finder) replies(b *board.Board, k int) []board.Move {
	if k <= 1 || f.exhaustive {
		return movegen.GenAll(b, nil)
	}
	return movegen.Gen(b, nil) // для глубины 3: только отсечённые ответы (эвристика)
}

// wallCands: мои стены-кандидаты. final=true: последняя стена в цепочке (должна давать выигрыш пути сама),
// иначе «подготовительная»: стены вблизи кратчайших путей соперника (в том числе почти без эффекта).
func (f *finder) wallCands(b *board.Board, final bool) []board.Move {
	if f.exhaustive {
		var out []board.Move
		for _, m := range movegen.GenAll(b, nil) {
			if m.Kind() != board.KindPawn {
				out = append(out, m)
			}
		}
		return out
	}
	if final {
		var out []board.Move
		for _, m := range movegen.Gen(b, nil) {
			if m.Kind() != board.KindPawn {
				out = append(out, m)
			}
		}
		return out
	}
	return f.prepCands(b)
}

// prepCands: легальные стены, касающиеся клеток в радиусе 2 от кратчайших путей соперника.
func (f *finder) prepCands(b *board.Board) []board.Move {
	opp := 1 - int(b.Turn)
	d := path.Field(b, board.GoalRow(opp))
	start := int(b.P[opp])
	var onDag, mark [81]bool
	var q [81]uint8
	q[0], onDag[start] = uint8(start), true
	h, t := 0, 1
	for h < t {
		cur := int(q[h])
		h++
		r, c := cur/9, cur%9
		if d[cur] <= 0 {
			continue
		}
		for k := 0; k < 4; k++ {
			if !b.CanStep(r, c, k) {
				continue
			}
			n := (r+dr[k])*9 + c + dc[k]
			if d[n] == d[cur]-1 && !onDag[n] {
				onDag[n] = true
				q[t] = uint8(n)
				t++
			}
		}
	}
	const rad = 2
	for i := 0; i < 81; i++ {
		if !onDag[i] {
			continue
		}
		r, c := i/9, i%9
		for rr := r - rad; rr <= r+rad; rr++ {
			for cc := c - rad; cc <= c+rad; cc++ {
				if rr >= 0 && rr < 9 && cc >= 0 && cc < 9 {
					mark[rr*9+cc] = true
				}
			}
		}
	}
	var out []board.Move
	for r := 0; r < 8; r++ {
		for c := 0; c < 8; c++ {
			if !(mark[r*9+c] || mark[r*9+c+1] || mark[(r+1)*9+c] || mark[(r+1)*9+c+1]) {
				continue
			}
			if movegen.LegalWall(b, true, r, c) {
				out = append(out, board.WallMove(true, r, c))
			}
			if movegen.LegalWall(b, false, r, c) {
				out = append(out, board.WallMove(false, r, c))
			}
		}
	}
	return out
}

