// Package movegen: отсечённый генератор ходов с ленивой проверкой легальности стен.
//
// Идея:
//  1. Кандидаты-стены: те, что перекрывают ребро кратчайших путей соперника
//     (от его пешки к цели) или любое ребро у его пешки.
//  2. Правило контактов: стена может отрезать путь, только если хотя бы 2 из её
//     3 точек (концы и середина) касаются границы доски или уже стоящих стен.
//     При <2 контактов легальность (наличие пути) гарантирована без BFS.
//  3. Ленивость: Candidates отдаёт ходы без проверки путей, Legal доделывает её
//     только для стен с >=2 контактов; Gen = Candidates + Legal.
package movegen

import (
	"math/bits"

	"quoridor/board"
	"quoridor/path"
)

var dr = [4]int{-1, 1, 0, 0}
var dc = [4]int{0, 0, -1, 1}

func has(m uint64, r, c int) bool {
	if r < 0 || r > 7 || c < 0 || c > 7 {
		return false
	}
	return m&(1<<uint(r*8+c)) != 0
}

// Занятые точки решётки 10x10: бит j в occ[i] = точка на пересечении линий (i,j).
type occ [10]uint16

func mark(o *occ, i, j int) { o[i] |= 1 << uint(j) }

// pointsOf: концы и середина стены в координатах решётки.
func pointsOf(h bool, r, c int) [3][2]int {
	if h {
		return [3][2]int{{r + 1, c}, {r + 1, c + 1}, {r + 1, c + 2}}
	}
	return [3][2]int{{r, c + 1}, {r + 1, c + 1}, {r + 2, c + 1}}
}

func occupied(b *board.Board) (o occ) {
	for m := b.HW; m != 0; m &= m - 1 {
		i := tz(m)
		for _, p := range pointsOf(true, i/8, i%8) {
			mark(&o, p[0], p[1])
		}
	}
	for m := b.VW; m != 0; m &= m - 1 {
		i := tz(m)
		for _, p := range pointsOf(false, i/8, i%8) {
			mark(&o, p[0], p[1])
		}
	}
	return
}

func tz(m uint64) int { return bits.TrailingZeros64(m) }

func contacts(o *occ, h bool, r, c int) (n int) {
	for _, p := range pointsOf(h, r, c) {
		if p[0] == 0 || p[0] == 9 || p[1] == 0 || p[1] == 9 || o[p[0]]>>uint(p[1])&1 != 0 {
			n++
		}
	}
	return
}

// placeable: все проверки, кроме существования пути (границы, запас, перекрытие, крест).
func placeable(b *board.Board, h bool, r, c int) bool {
	if r < 0 || r > 7 || c < 0 || c > 7 || b.Walls[b.Turn] == 0 {
		return false
	}
	if h {
		return !(has(b.HW, r, c) || has(b.HW, r, c-1) || has(b.HW, r, c+1) || has(b.VW, r, c))
	}
	return !(has(b.VW, r, c) || has(b.VW, r-1, c) || has(b.VW, r+1, c) || has(b.HW, r, c))
}

// pathsOK: после установки стены оба игрока ещё имеют путь.
func pathsOK(b *board.Board, h bool, r, c int) bool {
	nb := *b
	if h {
		nb.HW |= 1 << uint(r*8+c)
	} else {
		nb.VW |= 1 << uint(r*8+c)
	}
	return path.Len(&nb, 0) >= 0 && path.Len(&nb, 1) >= 0
}

// LegalWall: то же, что board.WithWall(...).ok, но с правилом контактов.
func LegalWall(b *board.Board, h bool, r, c int) bool {
	if !placeable(b, h, r, c) {
		return false
	}
	o := occupied(b)
	if contacts(&o, h, r, c) < 2 {
		return true
	}
	return pathsOK(b, h, r, c)
}

// Legal: ленивая проверка хода из Candidates (пешечные ходы всегда легальны).
func Legal(b *board.Board, m board.Move) bool {
	if m.Kind() == board.KindPawn {
		return true
	}
	h := m.Kind() == board.KindH
	r, c := m.Idx()/8, m.Idx()%8
	o := occupied(b)
	if contacts(&o, h, r, c) < 2 {
		return true
	}
	return pathsOK(b, h, r, c)
}

// blockers помечает стены, перекрывающие шаг из (r,c) в направлении k.
func blockers(k, r, c int, hc, vc *uint64) {
	set := func(m *uint64, rr, cc int) {
		if rr >= 0 && rr <= 7 && cc >= 0 && cc <= 7 {
			*m |= 1 << uint(rr*8+cc)
		}
	}
	switch k {
	case board.Up:
		set(hc, r-1, c)
		set(hc, r-1, c-1)
	case board.Down:
		set(hc, r, c)
		set(hc, r, c-1)
	case board.Left:
		set(vc, r, c-1)
		set(vc, r-1, c-1)
	default:
		set(vc, r, c)
		set(vc, r-1, c)
	}
}

// wallCandidates: маски стен, которые стоит рассматривать (без проверки легальности).
// slack > 0 добавляет стены, перекрывающие рёбра путей, которые длиннее кратчайшего не более чем на slack
// (обходы): именно такие стены часто решают исход (закрывают запасной путь).
func wallCandidates(b *board.Board, slack, conn int) (hc, vc uint64) {
	opp := 1 - int(b.Turn)
	d := path.Field(b, board.GoalRow(opp))
	start := int(b.P[opp])
	var seen [81]bool
	var q [81]uint8
	q[0], seen[start] = uint8(start), true
	h, t := 0, 1
	for h < t {
		cur := int(q[h])
		h++
		r, c := cur/9, cur%9
		for k := 0; k < 4; k++ {
			if !b.CanStep(r, c, k) {
				continue
			}
			n := (r+dr[k])*9 + c + dc[k]
			// ребро на кратчайшем пути соперника, либо любое ребро у его пешки
			if (d[cur] > 0 && d[n] == d[cur]-1) || cur == start {
				blockers(k, r, c, &hc, &vc)
			}
			if d[cur] > 0 && d[n] == d[cur]-1 && !seen[n] {
				seen[n] = true
				q[t] = uint8(n)
				t++
			}
		}
	}
	if slack > 0 {
		wallCandidatesSlack(b, d, start, slack, &hc, &vc)
	}
	if conn > 0 { // «достраивающие» стены: касаются края или стоящих стен (продолжают барьер, закрывают проход)
		o := occupied(b)
		for r := 0; r < 8; r++ {
			for c := 0; c < 8; c++ {
				if contacts(&o, true, r, c) >= conn {
					hc |= 1 << uint(r*8+c)
				}
				if contacts(&o, false, r, c) >= conn {
					vc |= 1 << uint(r*8+c)
				}
			}
		}
	}
	return
}

// wallCandidatesSlack: рёбра на путях длиной <= кратчайший+slack (расстояния от пешки и до цели).
func wallCandidatesSlack(b *board.Board, d [81]int8, start, slack int, hc, vc *uint64) {
	var ds [81]int8
	for i := range ds {
		ds[i] = -1
	}
	var q [81]uint8
	q[0], ds[start] = uint8(start), 0
	h, t := 0, 1
	limit := int(d[start]) + slack
	for h < t {
		cur := int(q[h])
		h++
		r, c := cur/9, cur%9
		for k := 0; k < 4; k++ {
			if !b.CanStep(r, c, k) {
				continue
			}
			n := (r+dr[k])*9 + c + dc[k]
			if int(ds[cur])+1+int(d[n]) <= limit {
				blockers(k, r, c, hc, vc)
			}
			if ds[n] < 0 {
				ds[n] = ds[cur] + 1
				q[t] = uint8(n)
				t++
			}
		}
	}
}

// Candidates: ходы пешки + отсечённые стены, которые прошли дешёвые проверки
// (границы, запас, перекрытие, крест). Пути у стен с >=2 контактами НЕ проверены:
// перед применением вызывай Legal.
func Candidates(b *board.Board, out []board.Move) []board.Move { return CandidatesOpt(b, out, 0, 0) }

// CandidatesOpt: как Candidates, но с расширенным набором стен (см. wallCandidates):
// slack — обходные пути, conn — стены не менее чем с conn контактами.
func CandidatesOpt(b *board.Board, out []board.Move, slack, conn int) []board.Move {
	if b.Winner() >= 0 {
		return out
	}
	var buf [8]uint8
	for _, to := range b.PawnMoves(int(b.Turn), buf[:0]) {
		out = append(out, board.PawnMove(int(to)))
	}
	if b.Walls[b.Turn] == 0 {
		return out
	}
	hc, vc := wallCandidates(b, slack, conn)
	for m := hc; m != 0; m &= m - 1 {
		i := tz(m)
		if placeable(b, true, i/8, i%8) {
			out = append(out, board.WallMove(true, i/8, i%8))
		}
	}
	for m := vc; m != 0; m &= m - 1 {
		i := tz(m)
		if placeable(b, false, i/8, i%8) {
			out = append(out, board.WallMove(false, i/8, i%8))
		}
	}
	return out
}

// Gen: полностью легальные отсечённые ходы.
func Gen(b *board.Board, out []board.Move) []board.Move {
	start := len(out)
	out = Candidates(b, out)
	w := start
	for _, m := range out[start:] {
		if Legal(b, m) {
			out[w] = m
			w++
		}
	}
	return out[:w]
}

// GenAll: ВСЕ легальные ходы (без отсечения кандидатов), но быстро: стены через LegalWall
// (правило контактов), поэтому в десятки раз быстрее board.Moves. Результат совпадает с board.Moves.
func GenAll(b *board.Board, out []board.Move) []board.Move {
	if b.Winner() >= 0 {
		return out
	}
	var buf [8]uint8
	for _, to := range b.PawnMoves(int(b.Turn), buf[:0]) {
		out = append(out, board.PawnMove(int(to)))
	}
	if b.Walls[b.Turn] == 0 {
		return out
	}
	for r := 0; r < 8; r++ {
		for c := 0; c < 8; c++ {
			if LegalWall(b, true, r, c) {
				out = append(out, board.WallMove(true, r, c))
			}
			if LegalWall(b, false, r, c) {
				out = append(out, board.WallMove(false, r, c))
			}
		}
	}
	return out
}

// Contacts: сколько из трёх точек стены (концы и середина) касаются границы доски или уже стоящих стен.
func Contacts(b *board.Board, m board.Move) int {
	o := occupied(b)
	return contacts(&o, m.Kind() == board.KindH, m.Idx()/8, m.Idx()%8)
}

// CandidatesSplit: основные кандидаты (как Candidates) и затем «достраивающие» стены с >= conn контактов.
// Возвращает ходы и число основных (они идут первыми).
func CandidatesSplit(b *board.Board, out []board.Move, conn int) ([]board.Move, int) {
	start := len(out)
	out = Candidates(b, out)
	nBase := len(out) - start
	if conn <= 0 || b.Walls[b.Turn] == 0 || b.Winner() >= 0 {
		return out, nBase
	}
	o := occupied(b)
	for r := 0; r < 8; r++ {
		for c := 0; c < 8; c++ {
			for _, h := range [2]bool{true, false} {
				if contacts(&o, h, r, c) < conn || !placeable(b, h, r, c) {
					continue
				}
				m := board.WallMove(h, r, c)
				dup := false
				for _, x := range out[start : start+nBase] {
					if x == m {
						dup = true
						break
					}
				}
				if !dup {
					out = append(out, m)
				}
			}
		}
	}
	return out, nBase
}

