package learn

import (
	"math"
	"math/rand"
	"testing"

	"quoridor/board"
	"quoridor/eval"
	"quoridor/match"
	"quoridor/movegen"
	"quoridor/search"
)

// Синтетика: исходы генерируются по «истинным» весам, отличающимся от стартовых;
// подбор должен приблизить предсказания к истине (потеря на отложенных данных падает).
func TestFitRecoversDirection(t *testing.T) {
	truth := eval.DefaultV2()
	truth.Walls, truth.Exit, truth.Flex = 260, 90, 12
	start := eval.DefaultV2()
	rng := rand.New(rand.NewSource(1))
	wt := truth.Vector()
	var ss []Sample
	for g := 0; g < 300; g++ {
		b := board.New()
		for ply := 0; ply < 70 && b.Winner() < 0; ply++ {
			if f, ok := eval.Features(&b); ok {
				dot := 0.0
				for i := range f {
					dot += wt[i] * f[i]
				}
				y := 0.0
				if rng.Float64() < 1/(1+math.Exp(-dot/300)) {
					y = 1
				}
				ss = append(ss, Sample{F: f, Y: y, Game: g})
			}
			ms := movegen.Gen(&b, nil)
			b = b.Apply(ms[rng.Intn(len(ms))])
		}
	}
	cut := len(ss) * 4 / 5
	train, test := ss[:cut], ss[cut:]
	fit := Fit(train, start, Options{Reg: 0.002})
	ls, lf := Loss(test, start.Vector(), 300), Loss(test, fit.Vector(), 300)
	t.Logf("samples %d; test loss start %.4f fitted %.4f; weights %v", len(ss), ls, lf, fit)
	if lf >= ls {
		t.Fatalf("fit did not reduce held-out loss: %.4f -> %.4f", ls, lf)
	}
	// веса сдвинулись в сторону истинных
	if !(fit.Walls > start.Walls) || !(fit.Exit > start.Exit) {
		t.Errorf("weights did not move toward truth: %v", fit)
	}
}

func TestFitStaysPutWithoutData(t *testing.T) {
	w0 := eval.DefaultV2()
	if got := Fit(nil, w0, Options{}); got != w0 {
		t.Fatalf("no data must not change weights: %v", got)
	}
	// маленькая выборка с сильным притяжением тоже почти не двигает веса
	b := board.New()
	f, _ := eval.Features(&b)
	got := Fit([]Sample{{F: f, Y: 1}, {F: f, Y: 0}}, w0, Options{Reg: 5})
	if d := got.Walls - w0.Walls; d < -5 || d > 5 {
		t.Fatalf("weights drifted with strong prior: %v", got)
	}
}

func TestPipelineOnSelfPlayGames(t *testing.T) {
	fa, _ := match.BotFactory("v2", 2, 0, 14)
	fb, _ := match.BotFactory("v1", 2, 0, 14)
	n := 60
	if testing.Short() {
		n = 20
	}
	sc := match.Run(fa, fb, n, 4, 300, 2, 9)
	r := Run(sc.Games, eval.DefaultV2(), Options{Iters: 500})
	t.Logf("games %d samples %d: train %.4f -> %.4f, test %.4f -> %.4f, improves=%v, fitted %v",
		r.Games, r.Samples, r.BaseTrain, r.Train, r.BaseTest, r.Test, r.Improves, r.Fitted)
	if r.Train > r.BaseTrain+1e-9 {
		t.Fatalf("training loss must not increase: %.4f -> %.4f", r.BaseTrain, r.Train)
	}
	_ = search.Mate
}

