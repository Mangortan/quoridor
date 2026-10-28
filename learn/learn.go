// Package learn: обучение весов оценки v2 по результатам сыгранных партий (в том числе с человеком).
//
// Модель: вероятность победы ходящего в позиции = sigmoid(eval / Scale). Подбираем веса так, чтобы
// оценка лучше предсказывала исход партий (логистическая регрессия, как «Texel tuning»), с штрафом за
// отход от текущих весов: чем меньше данных, тем меньше веса сдвигаются.
package learn

import (
	"math"

	"quoridor/board"
	"quoridor/eval"
	"quoridor/match"
	"quoridor/tune"
)

type Sample struct {
	F    [eval.NumFeatures]float64
	Y    float64 // 1: ходящий в этой позиции в итоге выиграл, 0: проиграл
	Game int
}

// SamplesFromGames: по позиции перед каждым ходом решающих партий. Позиции чистой гонки пропускаются
// (оценка там точная). Ничьи не используются.
func SamplesFromGames(gs []match.Record) []Sample {
	var out []Sample
	for gi, g := range gs {
		if g.Winner < 0 {
			continue
		}
		ps, err := match.Replay(g)
		if err != nil {
			continue
		}
		for _, b := range ps {
			b := b
			f, ok := eval.Features(&b)
			if !ok {
				continue
			}
			y := 0.0
			if int(b.Turn) == g.Winner {
				y = 1
			}
			out = append(out, Sample{F: f, Y: y, Game: gi})
		}
	}
	return out
}

func sigmoid(x float64) float64 { return 1 / (1 + math.Exp(-x)) }

// Loss: средняя логарифмическая ошибка предсказания исхода при весах w.
func Loss(ss []Sample, w [eval.NumFeatures]float64, scale float64) float64 {
	if len(ss) == 0 {
		return 0
	}
	tot := 0.0
	for _, s := range ss {
		dot := 0.0
		for i := range w {
			dot += w[i] * s.F[i]
		}
		p := math.Min(math.Max(sigmoid(dot/scale), 1e-9), 1-1e-9)
		tot -= s.Y*math.Log(p) + (1-s.Y)*math.Log(1-p)
	}
	return tot / float64(len(ss))
}

type Options struct {
	Scale float64 // масштаб сигмоиды в единицах оценки (по умолчанию 300: 3 шага ≈ 73% победы)
	Reg   float64 // сила притяжения к исходным весам (по умолчанию 0.02)
	Iters int
}

func (o *Options) defaults() {
	if o.Scale == 0 {
		o.Scale = 300
	}
	if o.Reg == 0 {
		o.Reg = 0.02
	}
	if o.Iters == 0 {
		o.Iters = 3000
	}
}

// Fit подбирает веса (кроме D, он фиксирован: масштаб задаёт Scale), стартуя с w0.
// Параметры ограничены диапазонами tune.DefaultSpecs.
func Fit(ss []Sample, w0 eval.Params, opt Options) eval.Params {
	opt.defaults()
	specs := tune.DefaultSpecs()
	base := w0.Vector()
	idx := make([]int, 0, len(specs)) // индексы свободных признаков в векторе весов
	c := make([]float64, 0, len(specs))
	lo, hi := make([]float64, 0, len(specs)), make([]float64, 0, len(specs))
	for _, sp := range specs {
		for i, n := range eval.FeatureNames {
			if n == sp.Name {
				idx = append(idx, i)
				c = append(c, sp.C)
				lo, hi = append(lo, sp.Lo), append(hi, sp.Hi)
			}
		}
	}
	n := float64(len(ss))
	if n == 0 {
		return w0
	}
	z := make([]float64, len(idx)) // сдвиг в единицах C
	m, v := make([]float64, len(idx)), make([]float64, len(idx))
	const b1, b2, lr = 0.9, 0.999, 0.03
	cur := func() [eval.NumFeatures]float64 {
		w := base
		for k, i := range idx {
			w[i] = base[i] + c[k]*z[k]
		}
		return w
	}
	for it := 1; it <= opt.Iters; it++ {
		w := cur()
		grad := make([]float64, len(idx))
		for _, s := range ss {
			dot := 0.0
			for i := range w {
				dot += w[i] * s.F[i]
			}
			e := (sigmoid(dot/opt.Scale) - s.Y) / opt.Scale / n
			for k, i := range idx {
				grad[k] += e * c[k] * s.F[i]
			}
		}
		for k := range z {
			grad[k] += opt.Reg * z[k]
			m[k] = b1*m[k] + (1-b1)*grad[k]
			v[k] = b2*v[k] + (1-b2)*grad[k]*grad[k]
			mh, vh := m[k]/(1-math.Pow(b1, float64(it))), v[k]/(1-math.Pow(b2, float64(it)))
			z[k] -= lr * mh / (math.Sqrt(vh) + 1e-8)
			// границы
			val := base[idx[k]] + c[k]*z[k]
			if val < lo[k] {
				z[k] = (lo[k] - base[idx[k]]) / c[k]
			}
			if val > hi[k] {
				z[k] = (hi[k] - base[idx[k]]) / c[k]
			}
		}
	}
	return eval.FromVector(cur())
}

type Report struct {
	Samples, Games   int
	TrainN, TestN    int
	BaseTrain, Train float64 // потери на обучающей части: исходные веса и подобранные
	BaseTest, Test   float64 // то же на отложенных партиях (каждая 5-я)
	Fitted           eval.Params
	Improves         bool // отложенная ошибка уменьшилась заметно
}

// Run: делит партии на обучающие и отложенные (каждая 5-я), обучает на первых и честно проверяет на вторых.
// Итоговые веса затем обучаются на ВСЕХ партиях, но решение «улучшает ли» принимается по отложенным.
func Run(gs []match.Record, w0 eval.Params, opt Options) Report {
	opt.defaults()
	var train, test []match.Record
	for i, g := range gs {
		if i%5 == 4 {
			test = append(test, g)
		} else {
			train = append(train, g)
		}
	}
	tr, te := SamplesFromGames(train), SamplesFromGames(test)
	fit := Fit(tr, w0, opt)
	r := Report{Games: len(gs), TrainN: len(tr), TestN: len(te), Samples: len(tr) + len(te)}
	b0, b1 := w0.Vector(), fit.Vector()
	r.BaseTrain, r.Train = Loss(tr, b0, opt.Scale), Loss(tr, b1, opt.Scale)
	r.BaseTest, r.Test = Loss(te, b0, opt.Scale), Loss(te, b1, opt.Scale)
	r.Improves = len(te) > 0 && r.Test < r.BaseTest-0.002
	r.Fitted = Fit(append(tr, te...), w0, opt)
	return r
}

var _ = board.New

