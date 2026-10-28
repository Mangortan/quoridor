// Package board: базовое состояние Quoridor, правила и расстояния.
//
// Клетки: idx = row*9 + col, row/col в 0..8.
// Игрок 0 стартует в (0,4) и идёт к строке 8; игрок 1 стартует в (8,4) и идёт к строке 0.
//
// Стены: индекс бита = r*8 + c, r,c в 0..7.
//   HW (горизонтальная): лежит между строками r и r+1, закрывает столбцы c и c+1.
//   VW (вертикальная):   лежит между столбцами c и c+1, закрывает строки r и r+1.
package board

const (
	Up = iota
	Down
	Left
	Right
)

var dr = [4]int{-1, 1, 0, 0}
var dc = [4]int{0, 0, -1, 1}

// Для прыжка через пешку по направлению k боковые направления:
var sideDirs = [4][2]int{{Left, Right}, {Left, Right}, {Up, Down}, {Up, Down}}

type Board struct {
	HW, VW uint64
	P      [2]uint8 // позиции пешек
	Walls  [2]uint8 // стен в запасе
	Turn   uint8
}

func New() Board {
	return Board{P: [2]uint8{4, 76}, Walls: [2]uint8{10, 10}}
}

func GoalRow(p int) int {
	if p == 0 {
		return 8
	}
	return 0
}

func has(m uint64, r, c int) bool {
	if r < 0 || r > 7 || c < 0 || c > 7 {
		return false
	}
	return m&(1<<uint(r*8+c)) != 0
}

// CanStep: можно ли шагнуть из (r,c) в направлении d (границы и стены).
func (b *Board) CanStep(r, c, d int) bool {
	switch d {
	case Up:
		return r > 0 && !has(b.HW, r-1, c) && !has(b.HW, r-1, c-1)
	case Down:
		return r < 8 && !has(b.HW, r, c) && !has(b.HW, r, c-1)
	case Left:
		return c > 0 && !has(b.VW, r, c-1) && !has(b.VW, r-1, c-1)
	default: // Right
		return c < 8 && !has(b.VW, r, c) && !has(b.VW, r-1, c)
	}
}

// Dist: поле расстояний до строки goalRow без учёта пешек. -1 = недостижимо.
// Рёбра симметричны, поэтому достаточно BFS от целевой строки. Без аллокаций.
func (b *Board) Dist(goalRow int) [81]int8 {
	var d [81]int8
	for i := range d {
		d[i] = -1
	}
	var q [81]uint8
	h, t := 0, 0
	for c := 0; c < 9; c++ {
		i := goalRow*9 + c
		d[i] = 0
		q[t] = uint8(i)
		t++
	}
	for h < t {
		cur := int(q[h])
		h++
		r, c := cur/9, cur%9
		for k := 0; k < 4; k++ {
			if !b.CanStep(r, c, k) {
				continue
			}
			n := (r+dr[k])*9 + c + dc[k]
			if d[n] < 0 {
				d[n] = d[cur] + 1
				q[t] = uint8(n)
				t++
			}
		}
	}
	return d
}

// PawnMoves добавляет в out клетки, куда может пойти пешка p (с прыжками).
// Передавай срез с предвыделенной ёмкостью (cap >= 5), чтобы не было аллокаций.
func (b *Board) PawnMoves(p int, out []uint8) []uint8 {
	me, op := int(b.P[p]), int(b.P[1-p])
	r, c := me/9, me%9
	for k := 0; k < 4; k++ {
		if !b.CanStep(r, c, k) {
			continue
		}
		nr, nc := r+dr[k], c+dc[k]
		n := nr*9 + nc
		if n != op {
			out = append(out, uint8(n))
			continue
		}
		// Соперник рядом: прямой прыжок, если за ним нет преграды.
		if b.CanStep(nr, nc, k) {
			out = append(out, uint8((nr+dr[k])*9+nc+dc[k]))
			continue
		}
		// Иначе боковые прыжки.
		for _, s := range sideDirs[k] {
			if b.CanStep(nr, nc, s) {
				out = append(out, uint8((nr+dr[s])*9+nc+dc[s]))
			}
		}
	}
	return out
}

// WithWall возвращает позицию после стены игрока, чей ход, и ok=false, если стена нелегальна.
// Это медленная эталонная проверка (полный BFS обоих игроков): используй её для тестов,
// а в поиске добавь правило "≥2 контактов" и ленивую легальность.
func (b Board) WithWall(horizontal bool, r, c int) (Board, bool) {
	orig := b
	if r < 0 || r > 7 || c < 0 || c > 7 || b.Walls[b.Turn] == 0 {
		return orig, false
	}
	bit := uint64(1) << uint(r*8+c)
	if horizontal {
		if has(b.HW, r, c) || has(b.HW, r, c-1) || has(b.HW, r, c+1) || has(b.VW, r, c) {
			return orig, false
		}
		b.HW |= bit
	} else {
		if has(b.VW, r, c) || has(b.VW, r-1, c) || has(b.VW, r+1, c) || has(b.HW, r, c) {
			return orig, false
		}
		b.VW |= bit
	}
	b.Walls[b.Turn]--
	d0 := b.Dist(GoalRow(0))
	d1 := b.Dist(GoalRow(1))
	if d0[b.P[0]] < 0 || d1[b.P[1]] < 0 {
		return orig, false
	}
	return b, true
}






















