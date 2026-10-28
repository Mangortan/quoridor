// Command arena: матч двух ботов с SPRT, сохранением партий и дампом проигранных позиций.
//
//	go run ./cmd/arena -a v2 -b v1 -depth 3 -pairs 300 -sprt 0,30 -games games.txt -losses losses.txt
//	go run ./cmd/arena -a "v2:Walls=200" -b v2 -ms 30
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"quoridor/match"
)

func main() {
	a := flag.String("a", "v2", "бот A: walker | v1 | v2[:Name=Value,...]")
	b := flag.String("b", "v1", "бот B")
	depth := flag.Int("depth", 3, "фиксированная глубина (0 = по времени)")
	ms := flag.Int("ms", 0, "время на ход, мс (вместо глубины)")
	depthB := flag.Int("depth-b", -1, "глубина бота B (по умолчанию как у A)")
	msB := flag.Int("ms-b", -1, "мс на ход бота B (по умолчанию как у A)")
	pairs := flag.Int("pairs", 200, "максимум пар партий")
	batch := flag.Int("batch", 20, "размер пачки для проверки SPRT")
	opening := flag.Int("opening", 4, "случайных полуходов дебюта")
	maxPlies := flag.Int("maxplies", 300, "лимит полуходов (ничья)")
	sprt := flag.String("sprt", "0,30", "elo0,elo1 (alpha=beta=0.05); пусто = без SPRT, играть все пары")
	seed := flag.Int64("seed", 1, "seed дебютов")
	workers := flag.Int("workers", runtime.GOMAXPROCS(0), "параллельных партий")
	games := flag.String("games", "", "файл для сохранения партий")
	losses := flag.String("losses", "", "файл для дампа проигранных позиций")
	lossWho := flag.String("losses-who", "A", "чьи поражения сохранять: A или B")
	lossN := flag.Int("losses-last", 4, "сколько последних позиций проигравшего сохранять в партии")
	flag.Parse()

	if *depth == 0 && *ms == 0 {
		fmt.Fprintln(os.Stderr, "нужен -depth или -ms")
		os.Exit(2)
	}
	d := *depth
	if *ms > 0 {
		d = 0
	}
	fa, err := match.BotFactory(*a, d, *ms, 20)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	db, mb := d, *ms
	if *depthB >= 0 {
		db = *depthB
	}
	if *msB >= 0 {
		mb = *msB
		if mb > 0 {
			db = 0
		}
	}
	fb, err := match.BotFactory(*b, db, mb, 20)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	var res match.SPRTResult
	if *sprt == "" {
		sc := match.Run(fa, fb, *pairs, *opening, *maxPlies, *workers, *seed)
		res = match.SPRTResult{Score: sc, Verdict: "n/a"}
	} else {
		parts := strings.Split(*sprt, ",")
		e0, err0 := strconv.ParseFloat(parts[0], 64)
		var e1 float64
		var err1 error = fmt.Errorf("need elo0,elo1")
		if len(parts) == 2 {
			e1, err1 = strconv.ParseFloat(parts[1], 64)
		}
		if err0 != nil || err1 != nil {
			fmt.Fprintln(os.Stderr, "bad -sprt, want elo0,elo1")
			os.Exit(2)
		}
		res = match.RunSPRT(fa, fb, match.SPRT{Elo0: e0, Elo1: e1, Alpha: 0.05, Beta: 0.05}, *pairs, *batch, *opening, *maxPlies, *workers, *seed)
	}
	fmt.Printf("%s vs %s: %s (%v)\n", *a, *b, res, res.Score.Elapsed.Round(1e8))

	// RunSPRT не хранит партии (экономия памяти); для сохранения играем отдельно, если файл запрошен.
	if *games != "" || *losses != "" {
		sc := res.Score
		if len(sc.Games) == 0 {
			sc = match.Run(fa, fb, *pairs, *opening, *maxPlies, *workers, *seed)
		}
		if *games != "" {
			f, err := os.Create(*games)
			if err == nil {
				err = match.WriteGames(f, sc.Games)
				f.Close()
			}
			report(*games, len(sc.Games), err)
		}
		if *losses != "" {
			who := 0
			if strings.EqualFold(*lossWho, "B") {
				who = 1
			}
			ps := match.LossPositions(sc.Games, who, *lossN)
			f, err := os.Create(*losses)
			if err == nil {
				err = match.WritePositions(f, ps)
				f.Close()
			}
			report(*losses, len(ps), err)
		}
	}
}

func report(name string, n int, err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, name+":", err)
		return
	}
	fmt.Printf("saved %d records to %s\n", n, name)
}

