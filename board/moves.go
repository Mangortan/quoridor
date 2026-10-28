package board

// Move кодирует ход в uint16: старший байт — вид, младший — индекс.
//
//	KindPawn: idx = клетка назначения 0..80
//	KindH/KindV: idx = r*8+c, как индекс бита в HW/VW
type Move uint16

const (
	KindPawn = 0
	KindH    = 1
	KindV    = 2
)

func PawnMove(to int) Move { return Move(to) }

func WallMove(horizontal bool, r, c int) Move {
	k := KindV
	if horizontal {
		k = KindH
	}
	return Move(k<<8 | (r*8 + c))
}

func (m Move) Kind() int { return int(m >> 8) }
func (m Move) Idx() int  { return int(m & 0xff) }

// Winner: 0 или 1, если соответствующая пешка на целевой строке, иначе -1.
func (b *Board) Winner() int {
	for p := 0; p < 2; p++ {
		if int(b.P[p])/9 == GoalRow(p) {
			return p
		}
	}
	return -1
}

// Apply применяет ЛЕГАЛЬНЫЙ ход и передаёт ход сопернику.
// Легальность не проверяется (в горячем цикле поиска она уже известна из генератора).
func (b Board) Apply(m Move) Board {
	switch m.Kind() {
	case KindPawn:
		b.P[b.Turn] = uint8(m.Idx())
	case KindH:
		b.HW |= 1 << uint(m.Idx())
		b.Walls[b.Turn]--
	default:
		b.VW |= 1 << uint(m.Idx())
		b.Walls[b.Turn]--
	}
	b.Turn ^= 1
	return b
}

// Moves добавляет в out все легальные ходы игрока, чей ход.
// Стены проверяются медленной эталонной WithWall (полный BFS).
// В терминальной позиции ходов нет.
func (b *Board) Moves(out []Move) []Move {
	if b.Winner() >= 0 {
		return out
	}
	var buf [8]uint8
	for _, to := range b.PawnMoves(int(b.Turn), buf[:0]) {
		out = append(out, PawnMove(int(to)))
	}
	if b.Walls[b.Turn] == 0 {
		return out
	}
	for r := 0; r < 8; r++ {
		for c := 0; c < 8; c++ {
			if _, ok := b.WithWall(true, r, c); ok {
				out = append(out, WallMove(true, r, c))
			}
			if _, ok := b.WithWall(false, r, c); ok {
				out = append(out, WallMove(false, r, c))
			}
		}
	}
	return out
}

// Perft считает число листьев на глубине depth (терминальные позиции листьев не дают).
func Perft(b Board, depth int) uint64 {
	if depth == 0 {
		return 1
	}
	ms := b.Moves(make([]Move, 0, 160))
	if depth == 1 {
		return uint64(len(ms))
	}
	var n uint64
	for _, m := range ms {
		n += Perft(b.Apply(m), depth-1)
	}
	return n
}

