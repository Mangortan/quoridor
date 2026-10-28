// Command learn: подбирает веса оценки по сохранённым партиям (например, вашим партиям против бота).
//
//	go run ./cmd/learn -games games/human_games.txt
//	go run ./cmd/learn -games games/human_games.txt,selfplay.txt -validate 100
//
// Печатает отчёт и предлагаемые веса. Меняет ли оно игру к лучшему, решает отложенная выборка
// (каждая 5-я партия) и, с -validate, матч нового набора весов против текущего.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"quoridor/eval"
	"quoridor/learn"
	"quoridor/match"
	"quoridor/search"
)

func main() {
	files := flag.String("games", "", "файлы партий через запятую (формат match.WriteGames)")
	reg := flag.Float64("reg", 0, "сила притяжения к текущим весам (по умолчанию 0.02; больше = осторожнее)")
	scale := flag.Float64("scale", 0, "масштаб сигмоиды (по умолчанию 300)")
	validate := flag.Int("validate", 0, "число пар партий для матча «новые веса против текущих» (0 = не играть)")
	depth := flag.Int("depth", 3, "глубина ботов в проверочном матче")
	flag.Parse()
	if *files == "" {
		fmt.Fprintln(os.Stderr, "нужен -games файл[,файл...]")
		os.Exit(2)
	}
	var gs []match.Record
	for _, name := range strings.Split(*files, ",") {
		f, err := os.Open(strings.TrimSpace(name))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		g, err := match.ReadGames(f)
		f.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, name+":", err)
			os.Exit(1)
		}
		gs = append(gs, g...)
	}
	decisive := 0
	for _, g := range gs {
		if g.Winner >= 0 {
			decisive++
		}
	}
	fmt.Printf("партий: %d (решающих %d)\n", len(gs), decisive)
	if decisive < 10 {
		fmt.Println("Слишком мало решающих партий (нужно хотя бы 10, лучше 50+): веса не трогаю.")
		return
	}
	cur := eval.DefaultV2()
	r := learn.Run(gs, cur, learn.Options{Reg: *reg, Scale: *scale})
	fmt.Printf("позиций: обучение %d, отложенных %d\n", r.TrainN, r.TestN)
	fmt.Printf("ошибка предсказания исхода (меньше = лучше):\n  обучение:   %.4f -> %.4f\n  отложенные: %.4f -> %.4f\n", r.BaseTrain, r.Train, r.BaseTest, r.Test)
	fmt.Println("текущие веса:  ", cur)
	fmt.Println("подобранные:   ", r.Fitted)
	if !r.Improves {
		fmt.Println("Вывод: на отложенных партиях прогноз не стал заметно лучше. Менять веса не стоит (мало данных или они уже хороши).")
	} else {
		fmt.Println("Вывод: на отложенных партиях прогноз лучше. Можно проверить матчем (-validate) и подставить в DefaultV2.")
	}
	if *validate > 0 {
		mk := func(p eval.Params) func() match.Bot {
			return func() match.Bot {
				b := match.NewSearcher(18, search.Limits{MaxDepth: *depth})
				q := p
				b.S.Eval = q.Eval
				return b
			}
		}
		res := match.RunSPRT(mk(r.Fitted), mk(cur), match.SPRT{Elo0: 0, Elo1: 20, Alpha: 0.05, Beta: 0.05}, *validate, 20, 4, 300, runtime.GOMAXPROCS(0), 123)
		fmt.Println("матч «подобранные против текущих»:", res)
	}
}

