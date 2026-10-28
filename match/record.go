package match

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"quoridor/board"
)

// ---- запись ходов и позиций в текст ----

// FormatMove: пешка "p<клетка 0..80>", горизонтальная стена "h<r><c>", вертикальная "v<r><c>".
func FormatMove(m board.Move) string {
	switch m.Kind() {
	case board.KindPawn:
		return "p" + strconv.Itoa(m.Idx())
	case board.KindH:
		return fmt.Sprintf("h%d%d", m.Idx()/8, m.Idx()%8)
	default:
		return fmt.Sprintf("v%d%d", m.Idx()/8, m.Idx()%8)
	}
}

func ParseMove(s string) (board.Move, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("bad move %q", s)
	}
	switch s[0] {
	case 'p':
		n, err := strconv.Atoi(s[1:])
		if err != nil || n < 0 || n > 80 {
			return 0, fmt.Errorf("bad pawn move %q", s)
		}
		return board.PawnMove(n), nil
	case 'h', 'v':
		if len(s) != 3 || s[1] < '0' || s[1] > '7' || s[2] < '0' || s[2] > '7' {
			return 0, fmt.Errorf("bad wall move %q", s)
		}
		return board.WallMove(s[0] == 'h', int(s[1]-'0'), int(s[2]-'0')), nil
	}
	return 0, fmt.Errorf("bad move %q", s)
}

// String: "A=<сторона A> W=<победитель> ходы...". Партия воспроизводится с board.New().
func (r Record) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "A=%d W=%d", r.ASide, r.Winner)
	for _, m := range r.Moves {
		sb.WriteByte(' ')
		sb.WriteString(FormatMove(m))
	}
	return sb.String()
}

func ParseRecord(line string) (Record, error) {
	f := strings.Fields(line)
	if len(f) < 2 || !strings.HasPrefix(f[0], "A=") || !strings.HasPrefix(f[1], "W=") {
		return Record{}, fmt.Errorf("bad record header: %q", line)
	}
	a, err1 := strconv.Atoi(f[0][2:])
	w, err2 := strconv.Atoi(f[1][2:])
	if err1 != nil || err2 != nil {
		return Record{}, fmt.Errorf("bad record header: %q", line)
	}
	r := Record{ASide: a, Winner: w}
	for _, s := range f[2:] {
		m, err := ParseMove(s)
		if err != nil {
			return Record{}, err
		}
		r.Moves = append(r.Moves, m)
	}
	return r, nil
}

func WriteGames(w io.Writer, gs []Record) error {
	bw := bufio.NewWriter(w)
	for _, g := range gs {
		if _, err := bw.WriteString(g.String() + "\n"); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func ReadGames(r io.Reader) ([]Record, error) {
	var out []Record
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		g, err := ParseRecord(line)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, sc.Err()
}

// Replay: позиции ПЕРЕД каждым ходом партии (len == len(Moves)); проверяет заодно легальность ходов.
func Replay(r Record) ([]board.Board, error) {
	b := board.New()
	var out []board.Board
	for i, m := range r.Moves {
		out = append(out, b)
		if m.Kind() == board.KindPawn {
			ok := false
			var buf [8]uint8
			for _, to := range b.PawnMoves(int(b.Turn), buf[:0]) {
				if int(to) == m.Idx() {
					ok = true
				}
			}
			if !ok {
				return nil, fmt.Errorf("ply %d: illegal pawn move %s", i, FormatMove(m))
			}
		} else if _, ok := b.WithWall(m.Kind() == board.KindH, m.Idx()/8, m.Idx()%8); !ok {
			return nil, fmt.Errorf("ply %d: illegal wall %s", i, FormatMove(m))
		}
		b = b.Apply(m)
	}
	return out, nil
}

func FormatBoard(b board.Board) string {
	return fmt.Sprintf("HW=%#x VW=%#x P=%d,%d W=%d,%d T=%d", b.HW, b.VW, b.P[0], b.P[1], b.Walls[0], b.Walls[1], b.Turn)
}

func ParseBoard(s string) (board.Board, error) {
	var b board.Board
	var hw, vw uint64
	var p0, p1, w0, w1, t int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "HW=%v VW=%v P=%d,%d W=%d,%d T=%d", &hw, &vw, &p0, &p1, &w0, &w1, &t); err != nil {
		return b, fmt.Errorf("bad board %q: %v", s, err)
	}
	if p0 < 0 || p0 > 80 || p1 < 0 || p1 > 80 || w0 < 0 || w0 > 10 || w1 < 0 || w1 > 10 || (t != 0 && t != 1) {
		return b, fmt.Errorf("board out of range: %q", s)
	}
	b.HW, b.VW = hw, vw
	b.P = [2]uint8{uint8(p0), uint8(p1)}
	b.Walls = [2]uint8{uint8(w0), uint8(w1)}
	b.Turn = uint8(t)
	return b, nil
}

// ---- дамп проигранных позиций в регрессионный набор ----

type Position struct {
	Board  board.Board
	Played board.Move // что сыграл проигравший в этой позиции
	Game   int
	Ply    int
}

// LossPositions: для партий, которые проиграл бот who (0 = A, 1 = B), берёт его последние lastN
// позиций перед ходом (с ходом, который он сделал). Это материал для разбора и регрессионных тестов.
func LossPositions(gs []Record, who, lastN int) []Position {
	var out []Position
	for gi, g := range gs {
		if g.Winner < 0 || g.ASide < 0 {
			continue
		}
		side := g.ASide
		if who == 1 {
			side = 1 - g.ASide
		}
		if g.Winner == side { // этот бот выиграл
			continue
		}
		ps, err := Replay(g)
		if err != nil {
			continue
		}
		var mine []Position
		for i, b := range ps {
			if int(b.Turn) == side {
				mine = append(mine, Position{Board: b, Played: g.Moves[i], Game: gi, Ply: i})
			}
		}
		if len(mine) > lastN {
			mine = mine[len(mine)-lastN:]
		}
		out = append(out, mine...)
	}
	return out
}

func WritePositions(w io.Writer, ps []Position) error {
	bw := bufio.NewWriter(w)
	for _, p := range ps {
		if _, err := fmt.Fprintf(bw, "%s | %s | game %d ply %d\n", FormatBoard(p.Board), FormatMove(p.Played), p.Game, p.Ply); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func ReadPositions(r io.Reader) ([]Position, error) {
	var out []Position
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			return nil, fmt.Errorf("bad position line %q", line)
		}
		b, err := ParseBoard(parts[0])
		if err != nil {
			return nil, err
		}
		m, err := ParseMove(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, err
		}
		p := Position{Board: b, Played: m}
		if len(parts) > 2 {
			fmt.Sscanf(strings.TrimSpace(parts[2]), "game %d ply %d", &p.Game, &p.Ply)
		}
		out = append(out, p)
	}
	return out, sc.Err()
}

