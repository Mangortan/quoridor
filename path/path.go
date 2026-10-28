// Package path: быстрые поля расстояний (BFS на битовых рядах), без аллокаций.
//
// Доска 9x9 хранится как [9]uint16: бит c в ряду r = клетка (r,c).
// Из масок стен считаются маски заблокированных рёбер, затем BFS идёт слоями:
// на каждом слое все ряды расширяются побитовыми операциями.
package path

import (
	"math/bits"

	"quoridor/board"
)

const row9 = 0x1FF

type masks struct {
	hb [8]uint16 // hb[r], бит c: ребро (r,c)-(r+1,c) заблокировано
	vb [9]uint16 // vb[r], бит c: ребро (r,c)-(r,c+1) заблокировано (c<8)
}

func build(b *board.Board) (m masks) {
	var v [8]uint16
	for r := 0; r < 8; r++ {
		h := uint16((b.HW >> (uint(r) * 8)) & 0xFF) // стена c закрывает столбцы c и c+1
		m.hb[r] = h | h<<1
		v[r] = uint16((b.VW >> (uint(r) * 8)) & 0xFF) // стена r закрывает строки r и r+1
	}
	for r := 0; r < 9; r++ {
		var x uint16
		if r < 8 {
			x |= v[r]
		}
		if r > 0 {
			x |= v[r-1]
		}
		m.vb[r] = x
	}
	return
}

// step считает новый слой по текущему фронту f, не заходя в vis.
func (m *masks) step(f, vis *[9]uint16) (nf [9]uint16, any uint16) {
	for r := 0; r < 9; r++ {
		fr := f[r]
		x := ((fr&^m.vb[r])<<1)&row9 | (fr&^(m.vb[r]<<1))>>1
		if r > 0 {
			x |= f[r-1] &^ m.hb[r-1]
		}
		if r < 8 {
			x |= f[r+1] &^ m.hb[r]
		}
		x &^= vis[r]
		nf[r] = x
		any |= x
	}
	return
}

// Field: поле расстояний до строки goalRow, как board.Dist (-1 = недостижимо).
func Field(b *board.Board, goalRow int) [81]int8 {
	m := build(b)
	var d [81]int8
	for i := range d {
		d[i] = -1
	}
	var vis, f [9]uint16
	f[goalRow], vis[goalRow] = row9, row9
	for c := 0; c < 9; c++ {
		d[goalRow*9+c] = 0
	}
	for lvl := int8(1); ; lvl++ {
		nf, any := m.step(&f, &vis)
		if any == 0 {
			return d
		}
		for r := 0; r < 9; r++ {
			vis[r] |= nf[r]
			for y := nf[r]; y != 0; y &= y - 1 {
				d[r*9+bits.TrailingZeros16(y)] = lvl
			}
		}
		f = nf
	}
}

// Len: длина кратчайшего пути пешки игрока p до его цели (-1 = пути нет).
// Останавливается, как только дошёл до пешки, поэтому быстрее полного поля.
func Len(b *board.Board, p int) int {
	m := build(b)
	pr, pc := int(b.P[p])/9, uint(b.P[p])%9
	goal := board.GoalRow(p)
	if pr == goal {
		return 0
	}
	var vis, f [9]uint16
	f[goal], vis[goal] = row9, row9
	for lvl := 1; ; lvl++ {
		nf, any := m.step(&f, &vis)
		if any == 0 {
			return -1
		}
		if nf[pr]>>pc&1 != 0 {
			return lvl
		}
		for r := 0; r < 9; r++ {
			vis[r] |= nf[r]
		}
		f = nf
	}
}

