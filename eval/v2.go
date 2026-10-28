package eval

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"quoridor/board"
	"quoridor/path"
)

// Params: веса оценки v2. Все признаки считаются для обоих игроков и вычитаются («я минус соперник»).
type Params struct {
	D           int // шаг разницы путей
	Tempo       int // право хода
	Flex        int // гибкость: на каждые 1/4 бита log2(числа кратчайших путей)
	Cut         int // узкое место: слой DAG из одной клетки (стеной можно удлинить маршрут)
	Exit        int // «неудлиняемое» узкое ребро (покрыть его легальной стеной нельзя)
	Walls       int // стена в запасе
	WallsNarrow int // добавка к весу стены за каждое узкое место на путях обоих игроков
}

// DefaultV2: веса после SPSA (300 итераций self-play, глубина 3, cmd/tune), стартуя с ручных
// Tempo 20, Flex 3, Cut 0, Exit 20, Walls 170, WallsNarrow 3.
// Проверка против ручных весов (глубина 4, SPSA тренировался на глубине 3):
// 400 партий +61 Elo (H1), ещё 600 партий +26 Elo [-1, 54]; на глубине 3 разницы нет (+6, 1000 партий).
// Вес стены остался прежним (~170): SPSA подтвердил ручной подбор.
func DefaultV2() Params {
	return Params{D: 100, Tempo: 2, Flex: 0, Cut: 9, Exit: 42, Walls: 171, WallsNarrow: 0}
}

type side struct {
	d     int // длина кратчайшего пути пешки до цели
	logp  int // 4*log2(число кратчайших путей), с насыщением
	cut   int // слои DAG шириной 1 (не считая стартовой и целевой клеток)
	exit  int // одиночные рёбра между слоями, которые нельзя перекрыть ни одной стеной
	width int // максимальная ширина слоя
}

const pathCap = 1 << 20

var dr = [4]int{-1, 1, 0, 0}
var dc = [4]int{0, 0, -1, 1}

func has(m uint64, r, c int) bool {
	if r < 0 || r > 7 || c < 0 || c > 7 {
		return false
	}
	return m&(1<<uint(r*8+c)) != 0
}

// blockable: можно ли поставить хоть одну стену (по перекрытию/кресту, без учёта запаса и путей),
// перекрывающую шаг из (r,c) в направлении k.
func blockable(b *board.Board, r, c, k int) bool {
	try := func(h bool, wr, wc int) bool {
		if wr < 0 || wr > 7 || wc < 0 || wc > 7 {
			return false
		}
		if h {
			return !(has(b.HW, wr, wc) || has(b.HW, wr, wc-1) || has(b.HW, wr, wc+1) || has(b.VW, wr, wc))
		}
		return !(has(b.VW, wr, wc) || has(b.VW, wr-1, wc) || has(b.VW, wr+1, wc) || has(b.HW, wr, wc))
	}
	switch k {
	case board.Up:
		return try(true, r-1, c) || try(true, r-1, c-1)
	case board.Down:
		return try(true, r, c) || try(true, r, c-1)
	case board.Left:
		return try(false, r, c-1) || try(false, r-1, c-1)
	default:
		return try(false, r, c) || try(false, r-1, c)
	}
}

// analyze строит DAG кратчайших путей пешки игрока p (рёбра, где расстояние до цели убывает на 1).
func analyze(b *board.Board, p int) side {
	f := path.Field(b, board.GoalRow(p))
	start := int(b.P[p])
	D := int(f[start])
	var s side
	s.d = D
	if D <= 0 {
		s.logp = 0
		return s
	}
	var paths [81]uint32
	var seen [81]bool
	var q [81]uint8
	var layer [82]uint8
	var edges [82]uint8
	var lastCur [82]uint8
	var lastDir [82]uint8
	paths[start], seen[start] = 1, true
	q[0] = uint8(start)
	h, t := 0, 1
	for h < t {
		cur := int(q[h])
		h++
		r, c := cur/9, cur%9
		dcur := int(f[cur])
		layer[dcur]++
		if dcur == 0 {
			continue
		}
		for k := 0; k < 4; k++ {
			if !b.CanStep(r, c, k) {
				continue
			}
			n := (r+dr[k])*9 + c + dc[k]
			if int(f[n]) != dcur-1 {
				continue
			}
			if edges[dcur] == 0 {
				lastCur[dcur], lastDir[dcur] = uint8(cur), uint8(k)
			}
			edges[dcur]++
			if pv := paths[n] + paths[cur]; pv > pathCap {
				paths[n] = pathCap
			} else {
				paths[n] = pv
			}
			if !seen[n] {
				seen[n] = true
				q[t] = uint8(n)
				t++
			}
		}
	}
	total := uint32(0)
	for c := 0; c < 9; c++ {
		total += paths[board.GoalRow(p)*9+c]
	}
	if total > pathCap {
		total = pathCap
	}
	s.logp = int(4 * math.Log2(float64(total)))
	for d := 1; d <= D; d++ {
		if int(layer[d]) > s.width {
			s.width = int(layer[d])
		}
		if d < D && layer[d] == 1 {
			s.cut++
		}
		if edges[d] == 1 {
			cur := int(lastCur[d])
			if !blockable(b, cur/9, cur%9, int(lastDir[d])) {
				s.exit++
			}
		}
	}
	return s
}

// Eval: оценка v2 с точки зрения того, чей ход.
func (p *Params) Eval(b board.Board) int {
	me := int(b.Turn)
	wMe, wOp := int(b.Walls[me]), int(b.Walls[1-me])
	if wMe == 0 && wOp == 0 { // чистая гонка: точный исход (с точностью до блокировок пешками)
		dMe, dOp := path.Len(&b, me), path.Len(&b, 1-me)
		if dMe <= dOp {
			return 10000 - 10*dMe
		}
		return -(10000 - 10*dOp)
	}
	am, ao := analyze(&b, me), analyze(&b, 1-me)
	v := p.D*(ao.d-am.d) + p.Tempo
	v += p.Flex * (am.logp - ao.logp)
	cutOp, cutMe := 0, 0
	if wMe > 0 {
		cutOp = ao.cut // мои удары по его узким местам
	}
	if wOp > 0 {
		cutMe = am.cut // его удары по моим
	}
	v += p.Cut * (cutOp - cutMe)
	v += p.Exit * (am.exit - ao.exit)
	v += (p.Walls + p.WallsNarrow*(am.cut+ao.cut)) * (wMe - wOp)
	return v
}

// Set задаёт параметр по имени (для командной строки и SPSA).
func (p *Params) Set(name string, v int) bool {
	switch name {
	case "D":
		p.D = v
	case "Tempo":
		p.Tempo = v
	case "Flex":
		p.Flex = v
	case "Cut":
		p.Cut = v
	case "Exit":
		p.Exit = v
	case "Walls":
		p.Walls = v
	case "WallsNarrow":
		p.WallsNarrow = v
	default:
		return false
	}
	return true
}

func (p Params) String() string {
	return fmt.Sprintf("Params{D: %d, Tempo: %d, Flex: %d, Cut: %d, Exit: %d, Walls: %d, WallsNarrow: %d}",
		p.D, p.Tempo, p.Flex, p.Cut, p.Exit, p.Walls, p.WallsNarrow)
}

// ParseParams: "Flex=5,Walls=150" поверх DefaultV2.
func ParseParams(spec string) (Params, error) {
	p := DefaultV2()
	if strings.TrimSpace(spec) == "" {
		return p, nil
	}
	for _, kv := range strings.Split(spec, ",") {
		parts := strings.SplitN(strings.TrimSpace(kv), "=", 2)
		if len(parts) != 2 {
			return p, fmt.Errorf("bad param %q (want Name=Value)", kv)
		}
		v, err := strconv.Atoi(parts[1])
		if err != nil || !p.Set(parts[0], v) {
			return p, fmt.Errorf("bad param %q", kv)
		}
	}
	return p, nil
}

// NumFeatures — число линейных признаков оценки v2 (порядок как в Params.Vector).
const NumFeatures = 7

// FeatureNames: D, Tempo, Flex, Cut, Exit, Walls, WallsNarrow.
var FeatureNames = [NumFeatures]string{"D", "Tempo", "Flex", "Cut", "Exit", "Walls", "WallsNarrow"}

// Vector: веса в порядке FeatureNames.
func (p Params) Vector() [NumFeatures]float64 {
	return [NumFeatures]float64{float64(p.D), float64(p.Tempo), float64(p.Flex), float64(p.Cut), float64(p.Exit), float64(p.Walls), float64(p.WallsNarrow)}
}

// FromVector округляет веса до целых.
func FromVector(v [NumFeatures]float64) Params {
	r := func(x float64) int { return int(math.Round(x)) }
	return Params{D: r(v[0]), Tempo: r(v[1]), Flex: r(v[2]), Cut: r(v[3]), Exit: r(v[4]), Walls: r(v[5]), WallsNarrow: r(v[6])}
}

// Features: значения признаков с точки зрения того, чей ход, так что Eval(b) == sum(w[i]*f[i]).
// ok=false для позиций чистой гонки (обе без стен): там оценка точная, а не линейная.
func Features(b *board.Board) (f [NumFeatures]float64, ok bool) {
	me := int(b.Turn)
	wMe, wOp := int(b.Walls[me]), int(b.Walls[1-me])
	if wMe == 0 && wOp == 0 {
		return f, false
	}
	am, ao := analyze(b, me), analyze(b, 1-me)
	cutOp, cutMe := 0, 0
	if wMe > 0 {
		cutOp = ao.cut
	}
	if wOp > 0 {
		cutMe = am.cut
	}
	f[0] = float64(ao.d - am.d)
	f[1] = 1
	f[2] = float64(am.logp - ao.logp)
	f[3] = float64(cutOp - cutMe)
	f[4] = float64(am.exit - ao.exit)
	f[5] = float64(wMe - wOp)
	f[6] = float64((am.cut + ao.cut) * (wMe - wOp))
	return f, true
}

