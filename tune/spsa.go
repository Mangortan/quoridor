// Package tune: настройка весов оценки методом SPSA через матчи «theta+ против theta-».
package tune

import (
	"math"
	"math/rand"

	"quoridor/eval"
	"quoridor/match"
	"quoridor/search"
)

// Spec: один настраиваемый параметр.
type Spec struct {
	Name         string
	Init, Lo, Hi float64
	C            float64 // размах возмущения (в единицах параметра)
}

type SPSA struct {
	Specs []Spec
	Theta []float64
	LR    float64 // начальный шаг (в долях C за итерацию)
	Iters int     // ожидаемое число итераций (шаг убывает к ~LR/4)
	K     int
	rng   *rand.Rand
}

func New(specs []Spec, lr float64, iters int, seed int64) *SPSA {
	s := &SPSA{Specs: specs, LR: lr, Iters: iters, rng: rand.New(rand.NewSource(seed))}
	for _, sp := range specs {
		s.Theta = append(s.Theta, sp.Init)
	}
	return s
}

func (s *SPSA) clamp(i int, v float64) float64 {
	return math.Max(s.Specs[i].Lo, math.Min(s.Specs[i].Hi, v))
}

// Step делает одну итерацию. score(plus, minus) возвращает долю очков plus против minus в [0,1].
func (s *SPSA) Step(score func(plus, minus []float64) float64) (sc float64) {
	n := len(s.Theta)
	delta := make([]float64, n)
	plus := make([]float64, n)
	minus := make([]float64, n)
	ck := math.Pow(1+float64(s.K)/float64(s.Iters), -0.101) // слабое затухание возмущения
	for i := range s.Theta {
		delta[i] = 1
		if s.rng.Intn(2) == 0 {
			delta[i] = -1
		}
		c := s.Specs[i].C * ck
		plus[i] = s.clamp(i, s.Theta[i]+c*delta[i])
		minus[i] = s.clamp(i, s.Theta[i]-c*delta[i])
	}
	sc = score(plus, minus)
	// Шаг: двигаем theta в сторону победителя матча. (plus-minus)/2 = c*delta, поэтому
	// theta += lr * c*delta * (2*score-1): при ничьей (score=0.5) шага нет, при полной победе plus
	// сдвиг на lr*c в сторону plus. lr затухает с ростом итерации.
	lr := s.LR / math.Pow(1+3*float64(s.K)/float64(s.Iters), 0.6)
	for i := range s.Theta {
		s.Theta[i] = s.clamp(i, s.Theta[i]+lr*(plus[i]-minus[i])/2*(2*sc-1)*2)
	}
	s.K++
	return sc
}

// ---- привязка к eval.Params ----

func DefaultSpecs() []Spec {
	d := eval.DefaultV2()
	return []Spec{
		{"Tempo", float64(d.Tempo), 0, 100, 10},
		{"Flex", float64(d.Flex), 0, 20, 3},
		{"Cut", float64(d.Cut), 0, 40, 5},
		{"Exit", float64(d.Exit), 0, 80, 10},
		{"Walls", float64(d.Walls), 60, 350, 25},
		{"WallsNarrow", float64(d.WallsNarrow), 0, 20, 3},
	}
}

func ToParams(specs []Spec, theta []float64) eval.Params {
	p := eval.DefaultV2()
	for i, sp := range specs {
		p.Set(sp.Name, int(math.Round(theta[i])))
	}
	return p
}

// MatchScore: функция оценки для SPSA — матч depth-фиксированных ботов plus против minus.
func MatchScore(specs []Spec, depth, pairs, openingPlies, workers int, seed func() int64) func(plus, minus []float64) float64 {
	return func(plus, minus []float64) float64 {
		pp, pm := ToParams(specs, plus), ToParams(specs, minus)
		mk := func(p eval.Params) func() match.Bot {
			return func() match.Bot {
				b := match.NewSearcher(16, search.Limits{MaxDepth: depth})
				q := p
				b.S.Eval = q.Eval
				return b
			}
		}
		sc := match.Run(mk(pp), mk(pm), pairs, openingPlies, 300, workers, seed())
		return sc.Rate()
	}
}

