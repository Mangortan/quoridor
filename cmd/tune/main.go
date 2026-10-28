// Command tune: настройка весов eval.V2 методом SPSA (self-play theta+ против theta-).
//
//	go run ./cmd/tune -iters 150 -pairs 20 -depth 3
//
// Каждые -report итераций печатает текущий вектор как готовую строку eval.Params.
package main

import (
	"flag"
	"fmt"
	"runtime"

	"quoridor/tune"
)

func main() {
	iters := flag.Int("iters", 150, "число итераций SPSA")
	pairs := flag.Int("pairs", 20, "пар партий на итерацию (партий = 2*pairs)")
	depth := flag.Int("depth", 3, "глубина поиска ботов")
	opening := flag.Int("opening", 4, "случайных полуходов дебюта")
	lr := flag.Float64("lr", 1.0, "начальный шаг в долях размаха возмущения")
	seed := flag.Int64("seed", 1, "seed")
	workers := flag.Int("workers", runtime.GOMAXPROCS(0), "параллельных партий")
	report := flag.Int("report", 10, "как часто печатать вектор")
	flag.Parse()

	specs := tune.DefaultSpecs()
	s := tune.New(specs, *lr, *iters, *seed)
	it := int64(0)
	score := tune.MatchScore(specs, *depth, *pairs, *opening, *workers, func() int64 { it++; return *seed*100000 + it*997 })
	for k := 0; k < *iters; k++ {
		sc := s.Step(score)
		if (k+1)%*report == 0 || k+1 == *iters {
			fmt.Printf("iter %3d (last match %.2f): %s\n", k+1, sc, tune.ToParams(specs, s.Theta))
		}
	}
	fmt.Println("final:", tune.ToParams(specs, s.Theta))
}

