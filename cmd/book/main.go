// Command book: собирает книгу дебютов из записанных партий (например, ваших партий с ботом).
//
//	go run ./cmd/book -games games/human_games.txt -plies 16 -out book.txt
//	go run ./cmd/book -games games/human_games.txt -wins -plies 12 -out book.txt   # только выигранные вами
//	go run ./cmd/book -games games/human_games.txt -advance 0 -out book.txt        # без фильтра «3 шага вперёд»
//
// По умолчанию партии, где ваши первые 3 хода не все шаги пешкой вперёд, пропускаются (-advance 0 выключает фильтр).
// Из каждой партии берутся первые -plies полуходов, а в книгу идут ходы той стороны, за которую
// играли вы (поле A= в записи партии). Результат дописывается к существующему -out без дублей.
// Каждая линия в книге работает и в зеркальном виде (слева направо); отключить для линии можно словом nomirror.
// Проверить, где партия идёт по книге: go run ./cmd/book -out book.txt -check "p13 p67 p22 ..."
// Дальше бот использует книгу так: -a 'v2@book=book.txt' (arena) или -book book.txt (server).
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"quoridor/board"
	"quoridor/match"
)

// checkLine показывает, где партия идёт по книге (с учётом зеркала), а где выходит из неё.
func checkLine(bookPath, line string) {
	f, err := os.Open(bookPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bk, err := match.ReadBook(f)
	f.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, moves, ok, err := match.ParseBookLine(line)
	if err != nil || !ok {
		fmt.Fprintln(os.Stderr, "не разобрал ходы:", err)
		os.Exit(2)
	}
	b := board.New()
	for i, m := range moves {
		who := "синие"
		if b.Turn == 1 {
			who = "красные"
		}
		var parts []string
		for _, bm := range bk.Lookup(&b) {
			parts = append(parts, fmt.Sprintf("%s×%d", match.FormatMove(bm.Move), bm.Weight))
		}
		status := "позиции нет в книге"
		if len(parts) > 0 {
			status = "книга: " + strings.Join(parts, " ")
		}
		fmt.Printf("%2d. %-7s %-4s  %s\n", i+1, who, match.FormatMove(m), status)
		b = b.Apply(m)
	}
}

// dropBadLines убирает из книги линии, попавшие в список плохих (без игр).
func dropBadLines(bookPath string, bad *match.BadList) {
	data, err := os.ReadFile(bookPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var kept []string
	dropped := 0
	for _, raw := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if side, mv, ok, err := match.ParseBookLine(raw); err == nil && ok && side >= 0 && bad.Bad(side, mv) {
			dropped++
			continue
		}
		kept = append(kept, raw)
	}
	if dropped > 0 { // резервная копия только когда что-то убираем
		os.WriteFile(bookPath+".bak", data, 0o644)
		os.WriteFile(bookPath, []byte(strings.Join(kept, "\n")+"\n"), 0o644)
	}
	fmt.Printf("убрано линий из списка плохих (%d в списке): %d\n", bad.Len(), dropped)
}

// validateBook играет каждую ещё не проверенную линию книги самоигрой и показывает, как позиция в её конце
// складывается у владельца. Линии из списка плохих сразу выбрасываются без игр, а уже проверенные
// (с отметкой «проверено») пропускаются, пока не указан -revalidate. С -prune плохие линии (доля очков < minScore)
// убираются из книги и записываются в список плохих, чтобы больше не проверять и не добавлять их.
func validateBook(bookPath, badPath string, bad *match.BadList, botSpec string, depth, ms, games, randPlies int, minScore float64, prune, revalidate bool, workers int) {
	data, err := os.ReadFile(bookPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	const mark = "  # проверено:"
	type item struct {
		raw   string
		job   int // индекс в jobs или -1, если линию не проверяем
		moves int
		known bool // в списке плохих
	}
	var items []item
	var jobs []match.ValidateJob
	already := 0
	for _, orig := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		raw, checked := orig, strings.Contains(orig, mark)
		if i := strings.Index(raw, mark); i >= 0 {
			raw = raw[:i] // отметку проверки заменим новой, если проверяем заново
		}
		side, mv, ok, err := match.ParseBookLine(raw)
		it := item{raw: raw, job: -1}
		switch {
		case err != nil || !ok || side < 0:
			it.raw = orig
		case bad.Bad(side, mv):
			it.known = true
		case checked && !revalidate:
			already++
			it.raw = orig // прежняя отметка остаётся
		default:
			it.job, it.moves = len(jobs), len(mv)
			jobs = append(jobs, match.ValidateJob{Moves: mv, Side: side})
		}
		items = append(items, it)
	}
	knownBad := 0
	for _, it := range items {
		if it.known {
			knownBad++
		}
	}
	fmt.Printf("линий в списке плохих (выбрасываются без игр): %d; уже проверенных (пропущены, -revalidate чтобы перепроверить): %d; к проверке: %d\n", knownBad, already, len(jobs))

	var stats []match.LineStat
	if len(jobs) > 0 {
		d := depth
		if ms > 0 {
			d = 0
		}
		f, err := match.BotFactory(botSpec, d, ms, 18)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "проверяю %d линий по %d партий (%d потоков)...\n", len(jobs), games, workers)
		last := 0
		stats = match.ValidateLines(f, jobs, games, randPlies, 300, workers, time.Now().UnixNano()%1000000, func(done, total int) {
			if done*20/total > last {
				last = done * 20 / total
				fmt.Fprintf(os.Stderr, "  %d%%\n", last*5)
			}
		})
	}

	var kept []string
	nbad := 0
	type newBad struct {
		side  int
		moves []board.Move
		note  string
	}
	var found []newBad
	for _, it := range items {
		if it.known {
			continue
		}
		if it.job < 0 {
			kept = append(kept, it.raw)
			continue
		}
		st := stats[it.job]
		isBad := st.Score() < minScore
		tag := "ok   "
		if isBad {
			tag = "ПЛОХО"
			nbad++
		}
		fmt.Printf("%s сторона %d, %2d ходов: очков %.2f ±%.2f (+%d -%d =%d)  %s\n", tag, jobs[it.job].Side, it.moves, st.Score(), st.SE(), st.Wins, st.Losses, st.Draws, shorten(it.raw))
		note := fmt.Sprintf("%.2f очков за %d партий", st.Score(), st.Games)
		if isBad {
			found = append(found, newBad{jobs[it.job].Side, jobs[it.job].Moves, note})
		} else {
			kept = append(kept, it.raw+mark+" "+note)
		}
	}
	fmt.Printf("плохих линий среди проверенных: %d из %d (порог %.2f); из списка плохих выброшено: %d\n", nbad, len(jobs), minScore, knownBad)
	if !prune {
		if nbad+knownBad > 0 {
			fmt.Println("чтобы убрать их из книги и записать в список плохих, добавьте -prune")
		}
		return
	}
	if len(found)+knownBad > 0 { // копию делаем только когда что-то убираем, чтобы не затереть прежнюю
		os.WriteFile(bookPath+".bak", data, 0o644)
	}
	os.WriteFile(bookPath, []byte(strings.Join(kept, "\n")+"\n"), 0o644)
	for _, nb := range found {
		if err := bad.AppendBad(badPath, nb.side, nb.moves, nb.note); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	fmt.Printf("книга переписана: оставлено %d линий; список плохих (%s): %d линий (старая книга — в %s.bak, если что-то убрано)\n", len(kept), badPath, bad.Len(), bookPath)
}

func shorten(s string) string {
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > 70 {
		s = s[:70] + "…"
	}
	return s
}

// startsOK: партия подходит, если у вашей стороны (ASide) первые n ходов — шаги пешкой вперёд.
// Если сторона неизвестна (A=-1), условие должно выполняться у обеих сторон.
func startsOK(g match.Record, n int) bool {
	if g.ASide >= 0 {
		return match.StartsWithAdvance(g.Moves, g.ASide, n)
	}
	return match.StartsWithAdvance(g.Moves, 0, n) && match.StartsWithAdvance(g.Moves, 1, n)
}

func main() {
	files := flag.String("games", "", "файлы партий через запятую")
	out := flag.String("out", "book.txt", "файл книги (дописывается)")
	plies := flag.Int("plies", 16, "сколько первых полуходов партии брать")
	wins := flag.Bool("wins", false, "брать только партии, выигранные стороной A (вашей)")
	advance := flag.Int("advance", 3, "брать партии, где первые N ходов вашей стороны — шаги пешкой вперёд (0 = брать любые партии)")
	validate := flag.Bool("validate", false, "проверить линии книги самоигрой и убрать проигрышные (см. -vgames, -minscore, -prune)")
	vgames := flag.Int("vgames", 30, "партий на линию при проверке")
	vbot := flag.String("vbot", "v2", "бот для проверки (без книги): walker | v1 | v2[:...]")
	vdepth := flag.Int("vdepth", 4, "глубина ботов при проверке (0 = по времени -vms)")
	vms := flag.Int("vms", 0, "мс на ход при проверке (0 = по глубине)")
	vrandom := flag.Int("vrandom", 2, "случайных полуходов после линии (для разнообразия партий)")
	minScore := flag.Float64("minscore", 0.45, "линия плоха, если её владелец набрал меньше этой доли очков")
	prune := flag.Bool("prune", false, "переписать -out без плохих линий (они попадают в список плохих, старая книга — в <out>.bak)")
	badPath := flag.String("bad", "", "список плохих дебютов (по умолчанию <out>.bad): линии из него не добавляются в книгу и убираются из неё")
	revalidate := flag.Bool("revalidate", false, "при -validate проверить заново и те линии, что уже проверялись")
	dropBad := flag.Bool("drop-bad", false, "убрать из -out линии, попавшие в список плохих, без новых проверок")
	workers := flag.Int("workers", runtime.GOMAXPROCS(0), "параллельных партий при проверке")
	check := flag.String("check", "", `проверить партию по книге -out: ходы через пробел, например "p13 p67 p22 p58"; для каждого хода покажет, что в книге в этой позиции`)
	flag.Parse()
	if *badPath == "" {
		*badPath = *out + ".bad"
	}
	if *check != "" {
		checkLine(*out, *check)
		return
	}
	bad, err := match.ReadBadList(*badPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *dropBad {
		dropBadLines(*out, bad)
		return
	}
	if *validate {
		validateBook(*out, *badPath, bad, *vbot, *vdepth, *vms, *vgames, *vrandom, *minScore, *prune, *revalidate, *workers)
		return
	}
	if *files == "" {
		fmt.Fprintln(os.Stderr, "нужен -games файл[,файл...]")
		os.Exit(2)
	}

	have := map[string]bool{}
	var old []string
	if data, err := os.ReadFile(*out); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(l) == "" {
				continue
			}
			old = append(old, l)
			if side, mv, ok, err := match.ParseBookLine(l); err == nil && ok {
				have[match.FormatBookLine(side, mv)] = true
				have[match.FormatBookLine(side, match.MirrorLine(mv))] = true // отражённая линия — то же самое
			}
		}
	}

	var added []string
	seen, skipped, skippedAdv, skippedBad := 0, 0, 0, 0
	for _, name := range strings.Split(*files, ",") {
		f, err := os.Open(strings.TrimSpace(name))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		gs, err := match.ReadGames(f)
		f.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, name+":", err)
			os.Exit(1)
		}
		for _, g := range gs {
			seen++
			if *wins && (g.ASide < 0 || g.Winner != g.ASide) {
				skipped++
				continue
			}
			if !startsOK(g, *advance) {
				skippedAdv++
				continue
			}
			mv := g.Moves
			if len(mv) > *plies {
				mv = mv[:*plies]
			}
			if g.ASide >= 0 && bad.Bad(g.ASide, mv) {
				skippedBad++
				continue
			}
			line := match.FormatBookLine(g.ASide, mv)
			if have[line] {
				continue
			}
			have[line] = true
			have[match.FormatBookLine(g.ASide, match.MirrorLine(mv))] = true
			added = append(added, line)
		}
	}

	w, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bw := bufio.NewWriter(w)
	for _, l := range append(old, added...) {
		fmt.Fprintln(bw, l)
	}
	bw.Flush()
	w.Close()

	rf, _ := os.Open(*out)
	bk, err := match.ReadBook(rf)
	rf.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "в книге ошибка:", err)
		os.Exit(1)
	}
	fmt.Printf("партий просмотрено: %d (пропущено по -wins: %d, без %d шагов пешкой вперёд в начале: %d, из списка плохих: %d), новых линий: %d\n", seen, skipped, *advance, skippedAdv, skippedBad, len(added))
	fmt.Printf("в %s: %d линий, %d позиций\n", *out, bk.Lines, bk.Positions())
}

