package eval

import (
	"math"
	"math/rand"
	"testing"

	"quoridor/board"
	"quoridor/movegen"
)

func TestV2Symmetry(t *testing.T) {
	p := DefaultV2()
	rng := rand.New(rand.NewSource(3))
	for g := 0; g < 150; g++ {
		b := board.New()
		for ply := 0; ply < 80 && b.Winner() < 0; ply++ {
			m := mirror(b)
			if p.Eval(b) != p.Eval(m) {
				t.Fatalf("asymmetric v2: %d vs %d\n%+v", p.Eval(b), p.Eval(m), b)
			}
			ms := movegen.Gen(&b, nil)
			b = b.Apply(ms[rng.Intn(len(ms))])
		}
	}
}

func TestV2Start(t *testing.T) {
	p := DefaultV2()
	b := board.New()
	if got := p.Eval(b); got != p.Tempo {
		t.Fatalf("start eval = %d, want tempo %d", got, p.Tempo)
	}
}

// ---- эталон признаков: перебор по определению ----

func bfsFrom(b *board.Board, from int) (d [81]int) {
	for i := range d {
		d[i] = -1
	}
	d[from] = 0
	q := []int{from}
	dr := [4]int{-1, 1, 0, 0}
	dc := [4]int{0, 0, -1, 1}
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		for k := 0; k < 4; k++ {
			if !b.CanStep(cur/9, cur%9, k) {
				continue
			}
			n := (cur/9+dr[k])*9 + cur%9 + dc[k]
			if d[n] < 0 {
				d[n] = d[cur] + 1
				q = append(q, n)
			}
		}
	}
	return
}

// refAnalyze: слой = множество клеток x, лежащих на кратчайшем пути (dStart[x]+dGoal[x]==D).
func refAnalyze(b *board.Board, p int) (D, paths, cut, width int) {
	start := int(b.P[p])
	ds := bfsFrom(b, start)
	goal := board.GoalRow(p)
	// расстояние до цели: мультиисточник — через board.Dist (старая независимая реализация)
	dg := b.Dist(goal)
	D = int(dg[start])
	if D <= 0 {
		return
	}
	layer := make([]int, D+1)
	for x := 0; x < 81; x++ {
		if ds[x] >= 0 && int(dg[x]) >= 0 && ds[x]+int(dg[x]) == D {
			layer[ds[x]]++
		}
	}
	for i := 0; i <= D; i++ {
		if layer[i] > width {
			width = layer[i]
		}
		if i >= 1 && i < D && layer[i] == 1 {
			cut++
		}
	}
	// число путей перебором
	var rec func(cur int) int
	dr := [4]int{-1, 1, 0, 0}
	dc := [4]int{0, 0, -1, 1}
	rec = func(cur int) int {
		if int(dg[cur]) == 0 {
			return 1
		}
		n := 0
		for k := 0; k < 4; k++ {
			if !b.CanStep(cur/9, cur%9, k) {
				continue
			}
			nx := (cur/9+dr[k])*9 + cur%9 + dc[k]
			if dg[nx] == dg[cur]-1 {
				n += rec(nx)
			}
		}
		return n
	}
	paths = rec(start)
	return
}

func TestAnalyzeMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	checked := 0
	for g := 0; g < 120; g++ {
		b := board.New()
		for ply := 0; ply < 80 && b.Winner() < 0; ply++ {
			for p := 0; p < 2; p++ {
				D, paths, cut, width := refAnalyze(&b, p)
				if paths > 200000 {
					continue // слишком много путей для перебора
				}
				got := analyze(&b, p)
				// число путей: сравниваем 4*log2 с точностью до округления
				wantLog := 0
				if paths > 0 {
					wantLog = int(4 * log2(float64(paths)))
				}
				if got.d != D || got.cut != cut || got.width != width || got.logp != wantLog {
					t.Fatalf("p%d: got %+v want d=%d cut=%d width=%d logp=%d (paths=%d)\n%+v", p, got, D, cut, width, wantLog, paths, b)
				}
				checked++
			}
			ms := movegen.Gen(&b, nil)
			b = b.Apply(ms[rng.Intn(len(ms))])
		}
	}
	t.Logf("checked %d analyses", checked)
}

func BenchmarkV2(bm *testing.B) {
	p := DefaultV2()
	rng := rand.New(rand.NewSource(5))
	var ps []board.Board
	b := board.New()
	for len(ps) < 500 {
		ps = append(ps, b)
		ms := movegen.Gen(&b, nil)
		b = b.Apply(ms[rng.Intn(len(ms))])
		if b.Winner() >= 0 {
			b = board.New()
		}
	}
	bm.ResetTimer()
	s := 0
	for i := 0; i < bm.N; i++ {
		s += p.Eval(ps[i%len(ps)])
	}
	_ = s
}

func BenchmarkV1(bm *testing.B) {
	rng := rand.New(rand.NewSource(5))
	var ps []board.Board
	b := board.New()
	for len(ps) < 500 {
		ps = append(ps, b)
		ms := movegen.Gen(&b, nil)
		b = b.Apply(ms[rng.Intn(len(ms))])
		if b.Winner() >= 0 {
			b = board.New()
		}
	}
	bm.ResetTimer()
	s := 0
	for i := 0; i < bm.N; i++ {
		s += V1(&ps[i%len(ps)])
	}
	_ = s
}

func log2(x float64) float64 { return math.Log2(x) }

func TestFeaturesReproduceEval(t *testing.T) {
	p := DefaultV2()
	w := p.Vector()
	rng := rand.New(rand.NewSource(8))
	checked := 0
	for g := 0; g < 100; g++ {
		b := board.New()
		for ply := 0; ply < 80 && b.Winner() < 0; ply++ {
			if f, ok := Features(&b); ok {
				dot := 0.0
				for i := range f {
					dot += w[i] * f[i]
				}
				if int(math.Round(dot)) != p.Eval(b) {
					t.Fatalf("features dot %.1f != eval %d\n%+v", dot, p.Eval(b), b)
				}
				checked++
			}
			ms := movegen.Gen(&b, nil)
			b = b.Apply(ms[rng.Intn(len(ms))])
		}
	}
	if checked < 1000 {
		t.Fatalf("too few positions checked: %d", checked)
	}
	if FromVector(w) != p {
		t.Fatal("Vector/FromVector round trip")
	}
}

