package tune

import (
	"math"
	"math/rand"
	"testing"

	"quoridor/eval"
)

// Синтетический «матч»: исход определяется тем, чей theta ближе к скрытому оптимуму, плюс шум.
func TestSPSAConverges(t *testing.T) {
	opt := []float64{40, 8, 150}
	specs := []Spec{{"a", 0, -100, 100, 8}, {"b", 20, 0, 30, 2}, {"c", 300, 0, 400, 30}}
	rng := rand.New(rand.NewSource(1))
	dist := func(x []float64) float64 {
		d := 0.0
		for i := range x {
			z := (x[i] - opt[i]) / specs[i].C
			d += z * z
		}
		return d
	}
	s := New(specs, 1.0, 600, 2)
	for k := 0; k < 600; k++ {
		s.Step(func(plus, minus []float64) float64 {
			p := 1 / (1 + math.Exp(0.15*(dist(plus)-dist(minus)))) // ближе к оптимуму -> чаще побеждает
			// матч из 80 партий: биномиальный шум
			w := 0
			for g := 0; g < 80; g++ {
				if rng.Float64() < p {
					w++
				}
			}
			return float64(w) / 80
		})
	}
	for i := range opt {
		if math.Abs(s.Theta[i]-opt[i]) > 2.5*specs[i].C {
			t.Errorf("param %s = %.1f, optimum %.1f (C=%.1f)", specs[i].Name, s.Theta[i], opt[i], specs[i].C)
		}
	}
	t.Logf("theta = %.1f (optimum %v)", s.Theta, opt)
}

func TestToParams(t *testing.T) {
	sp := DefaultSpecs()
	th := make([]float64, len(sp))
	for i := range sp {
		th[i] = sp[i].Init
	}
	p := ToParams(sp, th)
	if p != eval.DefaultV2() {
		t.Fatalf("init theta must reproduce DefaultV2: %+v", p)
	}
	th[4] = 1e9 // за границей — зажимается только в SPSA, ToParams просто округляет
	_ = ToParams(sp, th)
}

