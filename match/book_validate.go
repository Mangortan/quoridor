package match

import (
	"math"
	"math/rand"
	"sync"

	"quoridor/board"
	"quoridor/movegen"
)

// RandomContinuation: линия line и ещё n случайных легальных полуходов от её конца.
func RandomContinuation(rng *rand.Rand, line []board.Move, n int) []board.Move {
	b := board.New()
	out := make([]board.Move, 0, len(line)+n)
	for _, m := range line {
		out = append(out, m)
		b = b.Apply(m)
	}
	for i := 0; i < n && b.Winner() < 0; i++ {
		ms := movegen.Gen(&b, nil)
		if len(ms) == 0 {
			break
		}
		m := ms[rng.Intn(len(ms))]
		out = append(out, m)
		b = b.Apply(m)
	}
	return out
}

// LineStat — как сыграла позиция в конце линии между двумя одинаковыми ботами, с точки зрения владельца линии.
type LineStat struct {
	Games, Wins, Losses, Draws int
}

// Score: доля очков владельца линии (победа 1, ничья 0.5).
func (s LineStat) Score() float64 {
	if s.Games == 0 {
		return 0.5
	}
	return (float64(s.Wins) + 0.5*float64(s.Draws)) / float64(s.Games)
}

// SE: стандартная ошибка Score (грубая, по биномиальной модели).
func (s LineStat) SE() float64 {
	if s.Games == 0 {
		return 0.5
	}
	p := s.Score()
	return math.Sqrt(p * (1 - p) / float64(s.Games))
}

// ValidateJob — одна линия для проверки: ходы с начала партии и сторона-владелец (0 или 1).
type ValidateJob struct {
	Moves []board.Move
	Side  int
}

// ValidateLines проверяет линии самоигрой: для каждой играется games партий из позиции в конце линии
// (плюс randPlies случайных полуходов для разнообразия) двумя ботами newBot() — без книги, поэтому
// результат показывает, насколько позиция хороша на самом деле, а не насколько она знакома.
// Линия, после которой владелец стабильно проигрывает, — «проигрышный дебют».
func ValidateLines(newBot func() Bot, jobs []ValidateJob, games, randPlies, maxPlies, workers int, seed int64, progress func(done, total int)) []LineStat {
	type task struct{ job, game int }
	stats := make([]LineStat, len(jobs))
	tasks := make(chan task)
	var mu sync.Mutex
	var wg sync.WaitGroup
	done, total := 0, len(jobs)*games
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range tasks {
				j := jobs[t.job]
				rng := rand.New(rand.NewSource(seed + int64(t.job)*100003 + int64(t.game)))
				op := RandomContinuation(rng, j.Moves, randPlies)
				rec := PlayGame(newBot(), newBot(), op, maxPlies)
				mu.Lock()
				st := &stats[t.job]
				st.Games++
				switch {
				case rec.Winner < 0:
					st.Draws++
				case rec.Winner == j.Side:
					st.Wins++
				default:
					st.Losses++
				}
				done++
				if progress != nil {
					progress(done, total)
				}
				mu.Unlock()
			}
		}()
	}
	for j := range jobs {
		for g := 0; g < games; g++ {
			tasks <- task{j, g}
		}
	}
	close(tasks)
	wg.Wait()
	return stats
}

