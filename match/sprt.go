package match

import (
	"fmt"
	"math"
)

// SPRT: последовательный тест отношения правдоподобия по Elo (H0: elo0, H1: elo1).
// Трёхисходная (W/D/L) нормальная аппроксимация LLR, как в fishtest.
type SPRT struct {
	Elo0, Elo1  float64
	Alpha, Beta float64
}

func eloToScore(elo float64) float64 { return 1 / (1 + math.Pow(10, -elo/400)) }

func scoreToElo(s float64) float64 {
	if s <= 0 {
		return math.Inf(-1)
	}
	if s >= 1 {
		return math.Inf(1)
	}
	return -400 * math.Log10(1/s-1)
}

// Bounds: LLR <= lo принимает H0, LLR >= hi принимает H1.
func (t SPRT) Bounds() (lo, hi float64) {
	return math.Log(t.Beta / (1 - t.Alpha)), math.Log((1 - t.Beta) / t.Alpha)
}

func (t SPRT) LLR(w, d, l int) float64 {
	n := float64(w + d + l)
	if n == 0 {
		return 0
	}
	s := (float64(w) + 0.5*float64(d)) / n
	v := (float64(w)*(1-s)*(1-s) + float64(d)*(0.5-s)*(0.5-s) + float64(l)*s*s) / n
	if v < 1e-6 {
		v = 1e-6
	}
	s0, s1 := eloToScore(t.Elo0), eloToScore(t.Elo1)
	return n * (s1 - s0) * (2*s - s0 - s1) / (2 * v)
}

// EloCI: Elo и 95%-доверительный интервал по W/D/L.
func EloCI(w, d, l int) (elo, lo, hi float64) {
	n := float64(w + d + l)
	if n == 0 {
		return 0, math.Inf(-1), math.Inf(1)
	}
	s := (float64(w) + 0.5*float64(d)) / n
	v := (float64(w)*(1-s)*(1-s) + float64(d)*(0.5-s)*(0.5-s) + float64(l)*s*s) / n
	se := math.Sqrt(v / n)
	return scoreToElo(s), scoreToElo(s - 1.96*se), scoreToElo(s + 1.96*se)
}

type SPRTResult struct {
	Score   Score
	LLR     float64
	Verdict string // "H1" (A сильнее), "H0" (не сильнее), "undecided"
}

func (r SPRTResult) String() string {
	e, lo, hi := EloCI(r.Score.AWins, r.Score.Draws, r.Score.BWins)
	return fmt.Sprintf("+%d -%d =%d  Elo %.0f [%.0f, %.0f]  LLR %.2f  verdict %s",
		r.Score.AWins, r.Score.BWins, r.Score.Draws, e, lo, hi, r.LLR, r.Verdict)
}

// RunSPRT играет партиями-парами пачками по batch, пока LLR не выйдет за границы или не кончатся maxPairs.
func RunSPRT(newA, newB func() Bot, t SPRT, maxPairs, batch, openingPlies, maxPlies, workers int, seed int64) SPRTResult {
	lo, hi := t.Bounds()
	var total Score
	res := SPRTResult{Verdict: "undecided"}
	for done := 0; done < maxPairs; done += batch {
		n := batch
		if done+n > maxPairs {
			n = maxPairs - done
		}
		sc := Run(newA, newB, n, openingPlies, maxPlies, workers, seed+int64(done)*7919)
		total.AWins += sc.AWins
		total.BWins += sc.BWins
		total.Draws += sc.Draws
		total.Elapsed += sc.Elapsed
		res.LLR = t.LLR(total.AWins, total.Draws, total.BWins)
		if res.LLR >= hi {
			res.Verdict = "H1"
			break
		}
		if res.LLR <= lo {
			res.Verdict = "H0"
			break
		}
	}
	res.Score = total
	return res
}

