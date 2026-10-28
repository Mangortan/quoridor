// Command puzzle: проверяет бота на позициях-паззлах (Red ходит и должен найти выигрыш).
//
//	go run ./cmd/puzzle -f cmd/puzzle/puzzles.txt -ms 100,1000,5000
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"quoridor/board"
	"quoridor/eval"
	"quoridor/match"
	"quoridor/movegen"
	"quoridor/search"
)

var rot bool // вид повёрнут на 180° (показан с точки зрения ходящего Blue)

// show: ход в координатах экрана (при повороте переводим обратно)
func show(m board.Move) string {
	if !rot {
		return match.FormatMove(m)
	}
	switch m.Kind() {
	case board.KindPawn:
		return fmt.Sprintf("p(%d,%d)", 8-m.Idx()/9, 8-m.Idx()%9)
	case board.KindH:
		return fmt.Sprintf("h%d%d", 7-m.Idx()/8, 7-m.Idx()%8)
	}
	return fmt.Sprintf("v%d%d", 7-m.Idx()/8, 7-m.Idx()%8)
}

func main() {
	file := flag.String("f", "cmd/puzzle/puzzles.txt", "файл паззлов")
	times := flag.String("ms", "100,1000", "времена поиска, мс (через запятую)")
	deep := flag.Int("deep", 0, "мс: глубокий поиск в позиции ПОСЛЕ ожидаемого хода (форсированный выигрыш Red даёт большую оценку)")
	only := flag.String("only", "", "только эти паззлы (через запятую)")
	slack := flag.Int("slack", 0, "расширение набора стен-кандидатов (см. movegen.CandidatesSlack)")
	conn := flag.Int("conn", 0, "добавлять стены с >= N контактами в кандидаты")
	cr := flag.Bool("cr", false, "достраивающие стены (-conn) искать мельче")
	all := flag.Bool("all", false, "для каждого паззла разобрать все ходы (значение после хода, глубина -d)")
	d := flag.Int("d", 4, "глубина для -all")
	flag.Parse()
	data, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var ms []int
	for _, t := range strings.Split(*times, ",") {
		v, _ := strconv.Atoi(t)
		ms = append(ms, v)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) != 6 && len(f) != 7 {
			fmt.Println("плохая строка:", line)
			continue
		}
		name := strings.TrimSpace(f[0])
		if *only != "" && !strings.Contains(","+*only+",", ","+name+",") {
			continue
		}
		b, expect, err := parse(f)
		if err != nil {
			fmt.Printf("%s: %v\n", name, err)
			continue
		}
		fmt.Printf("== %s  %s\n   ожидаемый ход %s\n", name, match.FormatBoard(b), show(expect))
		{
			in := false
			for _, m := range movegen.CandidatesOpt(&b, nil, *slack, *conn) {
				if m == expect {
					in = true
				}
			}
			cn := -1
			if expect.Kind() != board.KindPawn {
				cn = movegen.Contacts(&b, expect)
			}
			fmt.Printf("   ожидаемый ход среди кандидатов поиска: %v (контактов у стены: %d)\n", in, cn)
		}
		for _, t := range ms {
			s := search.New(22)
			s.WallSlack, s.WallConn, s.ConnReduce = *slack, *conn, *cr
			s.Eval = func(b board.Board) int { p := eval.DefaultV2(); return p.Eval(b) }
			r := s.Search(b, search.Limits{Time: time.Duration(t) * time.Millisecond})
			mark := "НЕТ"
			if r.Move == expect {
				mark = "да"
			}
			fmt.Printf("   %5d мс: ход %-4s оценка %6d глубина %2d  совпал: %s\n", t, show(r.Move), r.Score, r.Depth, mark)
		}
		if *deep > 0 {
			s := search.New(24)
			s.Eval = func(b board.Board) int { p := eval.DefaultV2(); return p.Eval(b) }
			r := s.Search(b.Apply(expect), search.Limits{Time: time.Duration(*deep) * time.Millisecond})
			fmt.Printf("   после ожидаемого хода, %d мс: для Red %d, глубина %d\n", *deep, -r.Score, r.Depth)
		}
		if *all {
			for _, m := range movegen.GenAll(&b, nil) {
				nb := b.Apply(m)
				s := search.New(20)
				s.Eval = func(b board.Board) int { p := eval.DefaultV2(); return p.Eval(b) }
				r := s.Search(nb, search.Limits{MaxDepth: *d})
				fmt.Printf("      %-4s -> для Red %6d\n", show(m), -r.Score)
			}
		}
	}
}

func parse(f []string) (b board.Board, expect board.Move, err error) {
	rot = len(f) == 7 && strings.TrimSpace(f[6]) == "rot"
	pt := func(s string) (int, error) {
		x := strings.Split(strings.TrimSpace(s), ",")
		if len(x) != 2 {
			return 0, fmt.Errorf("плохая клетка %q", s)
		}
		r, e1 := strconv.Atoi(x[0])
		c, e2 := strconv.Atoi(x[1])
		if e1 != nil || e2 != nil {
			return 0, fmt.Errorf("плохая клетка %q", s)
		}
		return r*9 + c, nil
	}
	b = board.New()
	b.HW, b.VW = 0, 0
	for _, w := range strings.Fields(f[1]) {
		m, e := match.ParseMove(w)
		if e != nil {
			return b, 0, e
		}
		if m.Kind() == board.KindH {
			b.HW |= 1 << uint(m.Idx())
		} else {
			b.VW |= 1 << uint(m.Idx())
		}
	}
	p0, e := pt(f[2])
	if e != nil {
		return b, 0, e
	}
	p1, e := pt(f[3])
	if e != nil {
		return b, 0, e
	}
	b.P = [2]uint8{uint8(p0), uint8(p1)}
	ws := strings.Split(strings.TrimSpace(f[4]), ",")
	w0, _ := strconv.Atoi(ws[0])
	w1, _ := strconv.Atoi(ws[1])
	b.Walls = [2]uint8{uint8(w0), uint8(w1)}
	b.Turn = 1
	blueMoves := strings.HasPrefix(strings.TrimSpace(f[5]), "b:")
	if blueMoves {
		b.Turn = 0
		f[5] = strings.TrimPrefix(strings.TrimSpace(f[5]), "b:")
	}
	used := strings.Count(f[1], "h") + strings.Count(f[1], "v")
	_ = used
	e0 := strings.TrimSpace(f[5])
	if strings.HasPrefix(e0, "p") {
		r, c := int(e0[1]-'0'), int(e0[2]-'0')
		expect = board.PawnMove(r*9 + c)
	} else {
		expect, err = match.ParseMove(e0)
		if err != nil {
			return b, 0, err
		}
	}
	if rot { // перевод вида «ходящий Blue внизу» в обычную систему (Blue сверху идёт вниз)
		b = rotate(b)
		expect = rotateMove(expect)
	}
	// проверки: стен на доске = 20 - остатки; ожидаемый ход легален; соперники имеют путь
	n := strings.Count(f[1], "h") + strings.Count(f[1], "v")
	if !strings.HasSuffix(strings.TrimSpace(f[0]), "~") && n != 20-int(w0)-int(w1) {
		return b, 0, fmt.Errorf("стен на доске %d, по остаткам должно быть %d", n, 20-w0-w1)
	}
	legal := false
	for _, m := range movegen.GenAll(&b, nil) {
		if m == expect {
			legal = true
		}
	}
	if !legal {
		return b, 0, fmt.Errorf("ожидаемый ход %s нелегален", match.FormatMove(expect))
	}
	var buf [8]uint8
	var cells []string
	for _, to := range b.PawnMoves(int(b.Turn), buf[:0]) {
		r, c := int(to)/9, int(to)%9
		if rot {
			r, c = 8-r, 8-c
		}
		cells = append(cells, fmt.Sprintf("%d,%d", r, c))
	}
	fmt.Printf("   ходы пешки ходящего: %s\n", strings.Join(cells, " "))
	return b, expect, nil
}

func rev64(x uint64) uint64 { // клетка стены i = r*8+c -> 63-i = (7-r)*8+(7-c)
	var o uint64
	for i := 0; i < 64; i++ {
		if x>>uint(i)&1 != 0 {
			o |= 1 << uint(63-i)
		}
	}
	return o
}

func rotate(b board.Board) board.Board {
	nb := b
	nb.HW, nb.VW = rev64(b.HW), rev64(b.VW)
	nb.P = [2]uint8{80 - b.P[0], 80 - b.P[1]}
	return nb
}

func rotateMove(m board.Move) board.Move {
	switch m.Kind() {
	case board.KindPawn:
		return board.PawnMove(80 - m.Idx())
	case board.KindH:
		return board.WallMove(true, 7-m.Idx()/8, 7-m.Idx()%8)
	}
	return board.WallMove(false, 7-m.Idx()/8, 7-m.Idx()%8)
}

