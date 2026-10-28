package match

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"quoridor/board"
	"quoridor/eval"
	"quoridor/search"
)

// BotFactory строит фабрику ботов по описанию:
//
//	walker                    — всегда идёт по кратчайшему пути
//	v1                        — поиск + eval.V1
//	v2                        — поиск + eval.V2 с DefaultV2
//	v2:Flex=5,Walls=150       — то же с переопределёнными весами
//	v2@book=book.txt          — любой из ботов + книга дебютов из файла (ходы по книге, дальше поиск)
//	v2@smp4                   — 4 потока поиска (Lazy SMP); также @tm (управление временем), @lmr
//	v2@ponder                 — бот думает на ходу соперника (см. Searcher.Ponder); сочетается с @book
//
// depth > 0 — фиксированная глубина, ms > 0 — время на ход.
var connTag = regexp.MustCompile(`@conn(\d+)`)
var slackTag = regexp.MustCompile(`@slack(\d+)`)
var smpTag = regexp.MustCompile(`@smp(\d+)`)
var lmrTag = regexp.MustCompile(`@lmr(\d+)`)

func BotFactory(spec string, depth, ms int, ttBits uint) (func() Bot, error) {
	spec = strings.ReplaceAll(spec, "@nobook", "") // тег сервера: не подмешивать книгу автоматически
	ponder := strings.Contains(spec, "@ponder")
	spec = strings.ReplaceAll(spec, "@ponder", "")
	var opts []func(*search.Searcher)
	if m := connTag.FindStringSubmatch(spec); m != nil { // @connN: достраивающие стены (N контактов)
		n, _ := strconv.Atoi(m[1])
		spec = strings.Replace(spec, m[0], "", 1)
		opts = append(opts, func(s *search.Searcher) { s.WallConn = n })
	}
	if m := slackTag.FindStringSubmatch(spec); m != nil { // @slackN: стены-кандидаты на обходных путях
		n, _ := strconv.Atoi(m[1])
		spec = strings.Replace(spec, m[0], "", 1)
		opts = append(opts, func(s *search.Searcher) { s.WallSlack = n })
	}
	if m := smpTag.FindStringSubmatch(spec); m != nil { // @smpN: N потоков поиска
		n, _ := strconv.Atoi(m[1])
		spec = strings.Replace(spec, m[0], "", 1)
		opts = append(opts, func(s *search.Searcher) { s.Threads = n })
	}
	if m := lmrTag.FindStringSubmatch(spec); m != nil { // @lmrN: LMR с порогом N просмотренных ходов
		from, _ := strconv.Atoi(m[1])
		spec = strings.Replace(spec, m[0], "", 1)
		opts = append(opts, func(s *search.Searcher) { s.LMR, s.LMRFrom = true, from })
	}
	for tag, fn := range map[string]func(*search.Searcher){
		"@pvs": func(s *search.Searcher) { s.PVS = true },
		"@lmr": func(s *search.Searcher) { s.LMR = true },
		"@tm":  func(s *search.Searcher) { s.TM = true },
		"@cr":  func(s *search.Searcher) { s.ConnReduce = true },
		"@ext": func(s *search.Searcher) { s.ExtendWalls = true },
	} {
		if strings.Contains(spec, tag) {
			fn := fn
			spec = strings.ReplaceAll(spec, tag, "")
			opts = append(opts, fn)
		}
	}
	spec, bookPath, hasBook := strings.Cut(spec, "@book=")
	f, err := botFactory(spec, depth, ms, ttBits)
	if err == nil && len(opts) > 0 {
		inner := f
		f = func() Bot {
			b := inner()
			if s := SearcherOf(b); s != nil {
				for _, o := range opts {
					o(s.S)
				}
			}
			return b
		}
	}
	if err == nil && ponder {
		inner := f
		f = func() Bot {
			b := inner()
			if s := SearcherOf(b); s != nil {
				s.Ponder = true
			}
			return b
		}
	}
	if err != nil || !hasBook {
		return f, err
	}
	fh, err := os.Open(bookPath)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	// линии из списка плохих дебютов (book.txt.bad) в книгу не попадают
	bad, err := ReadBadList(bookPath + ".bad")
	if err != nil {
		return nil, err
	}
	bk, _, err := ReadBookExcluding(fh, bad)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", bookPath, err)
	}
	// накопленные результаты ходов книги лежат рядом: book.txt.stats
	statsPath := bookPath + ".stats"
	if sf, err := os.Open(statsPath); err == nil {
		err = bk.LoadStats(sf)
		sf.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %v", statsPath, err)
		}
	}
	bk.SetStatsFile(statsPath)
	return func() Bot { return NewBookBot(bk, f()) }, nil
}

func botFactory(spec string, depth, ms int, ttBits uint) (func() Bot, error) {
	name, args, _ := strings.Cut(spec, ":")
	lim := search.Limits{MaxDepth: depth}
	if ms > 0 {
		lim.Time = time.Duration(ms) * time.Millisecond
	}
	switch name {
	case "walker":
		return func() Bot { return Walker{} }, nil
	case "v1":
		return func() Bot {
			s := NewSearcher(ttBits, lim)
			s.S.Eval = func(b board.Board) int { return eval.V1(&b) }
			return s
		}, nil
	case "v2":
		p, err := eval.ParseParams(args)
		if err != nil {
			return nil, err
		}
		return func() Bot {
			s := NewSearcher(ttBits, lim)
			pp := p
			s.S.Eval = pp.Eval
			return s
		}, nil
	}
	return nil, fmt.Errorf("unknown bot %q (walker | v1 | v2[:Name=Value,...])", spec)
}

