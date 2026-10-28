// Command bridge: локальный HTTP-мост между userscript'ом на barricade.gg и нашим ботом.
//
//	go run ./cmd/bridge                          # бот v2, 1000 мс на ход, 127.0.0.1:8090
//	go run ./cmd/bridge -bot v2 -ms 1500
//	go run ./cmd/bridge -bot 'v2@book=book.txt'  # с книгой дебютов
//
// Userscript шлёт позицию (формат match.FormatBoard), мост отвечает ходом.
// История позиций партии копится здесь, поэтому бот видит повторы так же, как в арене.
package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/bits"
	"net/http"
	"strings"
	"sync"
	"time"

	"quoridor/board"
	"quoridor/match"
	"quoridor/movegen"
	"quoridor/search"
)

//go:embed panel.html
var panelHTML []byte

type server struct {
	mu      sync.Mutex
	defSpec string
	defMs   int
	depth   int
	bots    map[string]match.Bot
	last    lastMove // последний ход (для панели управления)
	hist    []uint64 // хеши позиций партии подряд (по полуходам), без текущей
}

type moveReq struct {
	Board string `json:"board"`
	Bot   string `json:"bot"`
	Ms    *int   `json:"ms"`    // nil = как запущен сервер; 0 = ограничивать по глубине; >0 = по времени
	Depth *int   `json:"depth"` // используется, когда ms == 0
	Reset bool   `json:"reset"`
}

type moveResp struct {
	OK         bool    `json:"ok"`
	Error      string  `json:"error,omitempty"`
	Kind       string  `json:"kind,omitempty"` // pawn | wall
	Move       string  `json:"move,omitempty"`
	Row        int     `json:"row"`
	Col        int     `json:"col"`
	Horizontal bool    `json:"horizontal,omitempty"`
	Score      *int    `json:"score,omitempty"`
	Depth      int     `json:"depth,omitempty"`
	Nodes      uint64  `json:"nodes,omitempty"`
	Ms         float64 `json:"ms"`
	FromBook   bool    `json:"fromBook,omitempty"`
	// сколько раз соперник сыграл угаданный нами ход из скольких «думалок»
	PonderHits, PonderStarts int `json:",omitempty"`
}

func (s *server) bot(spec string, depth, ms int) (match.Bot, error) {
	if ms > 0 {
		depth = 0 // как в арене: ограничение по времени важнее глубины
	} else if depth <= 0 {
		depth = s.depth
	}
	key := fmt.Sprintf("%s|%d|%d", spec, depth, ms)
	if b, ok := s.bots[key]; ok {
		return b, nil
	}
	f, err := match.BotFactory(spec, depth, ms, 20)
	if err != nil {
		return nil, err
	}
	for k, old := range s.bots { // сменили бота: «думалки» прежних больше не нужны
		if sp, ok := old.(interface{ StopPonder() }); ok {
			sp.StopPonder()
		}
		delete(s.bots, k)
	}
	b := f()
	s.bots[key] = b
	return b, nil
}

// resetBots останавливает «думалку» и забывает ботов (новая партия).
func (s *server) resetBots() {
	for _, b := range s.bots {
		if sp, ok := b.(interface{ StopPonder() }); ok {
			sp.StopPonder()
		}
	}
	s.bots = map[string]match.Bot{}
	s.hist = nil
}

func legal(b *board.Board, m board.Move) bool {
	if m.Kind() == board.KindPawn {
		var buf [8]uint8
		for _, to := range b.PawnMoves(int(b.Turn), buf[:0]) {
			if int(to) == m.Idx() {
				return true
			}
		}
		return false
	}
	return b.Walls[b.Turn] > 0 && movegen.LegalWall(b, m.Kind() == board.KindH, m.Idx()/8, m.Idx()%8)
}

// layoutError проверяет, что расстановка стен возможна в принципе (ловит ошибки распознавания страницы).
func layoutError(b *board.Board) string {
	bit := func(m uint64, r, c int) bool {
		return r >= 0 && r < 8 && c >= 0 && c < 8 && (m>>uint(r*8+c))&1 == 1
	}
	for r := 0; r < 8; r++ {
		for c := 0; c < 8; c++ {
			if bit(b.HW, r, c) && (bit(b.HW, r, c+1) || bit(b.VW, r, c)) {
				return fmt.Sprintf("стена h%d%d пересекает или перекрывает соседнюю", r, c)
			}
			if bit(b.VW, r, c) && bit(b.VW, r+1, c) {
				return fmt.Sprintf("стена v%d%d перекрывает соседнюю", r, c)
			}
		}
	}
	placed := bits.OnesCount64(b.HW) + bits.OnesCount64(b.VW)
	if want := 20 - int(b.Walls[0]) - int(b.Walls[1]); placed != want {
		return fmt.Sprintf("на доске распознано стен: %d, а по счётчикам должно быть %d", placed, want)
	}
	return ""
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func cors(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
	h.Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	h.Set("Access-Control-Allow-Private-Network", "true")
}

func (s *server) handleMove(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var req moveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, moveResp{Error: "bad json: " + err.Error()})
		return
	}
	b, err := match.ParseBoard(req.Board)
	if err != nil {
		writeJSON(w, 200, moveResp{Error: err.Error()})
		return
	}
	if b.Winner() >= 0 {
		writeJSON(w, 200, moveResp{Error: "партия уже окончена"})
		return
	}
	if msg := layoutError(&b); msg != "" {
		writeJSON(w, 200, moveResp{Error: msg})
		return
	}
	if b.P[0] == b.P[1] || b.Dist(8)[b.P[0]] < 0 || b.Dist(0)[b.P[1]] < 0 {
		writeJSON(w, 200, moveResp{Error: "некорректная позиция: пешки совпадают или нет пути к цели"})
		return
	}

	spec, ms, depth := req.Bot, s.defMs, s.depth
	if spec == "" {
		spec = s.defSpec
	}
	if req.Ms != nil {
		ms = *req.Ms
	}
	if req.Depth != nil {
		depth = *req.Depth
	}
	if ms < 0 || ms > 120000 || depth < 0 || depth > 14 {
		writeJSON(w, 200, moveResp{Error: "ms должно быть 0..120000, глубина 0..14"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if req.Reset {
		s.resetBots()
	}
	bot, err := s.bot(spec, depth, ms)
	if err != nil {
		writeJSON(w, 200, moveResp{Error: err.Error()})
		return
	}
	if ha, ok := bot.(interface{ SetHistory([]uint64) }); ok {
		ha.SetHistory(s.hist)
	}
	t0 := time.Now()
	m := bot.Choose(b)
	el := time.Since(t0)
	if !legal(&b, m) {
		writeJSON(w, 200, moveResp{Error: "бот вернул нелегальный ход " + match.FormatMove(m)})
		return
	}
	nb := b.Apply(m)
	s.hist = append(s.hist, search.Hash(&b), search.Hash(&nb))
	if len(s.hist) > 80 {
		s.hist = s.hist[len(s.hist)-80:]
	}

	resp := moveResp{OK: true, Move: match.FormatMove(m), Ms: float64(el.Microseconds()) / 1000}
	if m.Kind() == board.KindPawn {
		resp.Kind, resp.Row, resp.Col = "pawn", m.Idx()/9, m.Idx()%9
	} else {
		resp.Kind, resp.Row, resp.Col = "wall", m.Idx()/8, m.Idx()%8
		resp.Horizontal = m.Kind() == board.KindH
	}
	if sr := match.SearcherOf(bot); sr != nil {
		sc := sr.Last.Score
		resp.Score, resp.Depth, resp.Nodes = &sc, sr.Last.Depth, uint64(sr.Last.Nodes)
	}
	if bb, ok := bot.(*match.BookBot); ok {
		resp.FromBook = bb.FromBook
	}
	if sr := match.SearcherOf(bot); sr != nil && sr.Ponder {
		resp.PonderHits, resp.PonderStarts = sr.PonderHits, sr.PonderStarts
	}
	s.last = lastMove{N: s.last.N + 1, Move: resp.Move, Depth: resp.Depth, Ms: resp.Ms, Nodes: resp.Nodes, Score: resp.Score, FromBook: resp.FromBook, Hits: resp.PonderHits, Starts: resp.PonderStarts, At: time.Now().Format("15:04:05")}
	log.Printf("%s  %-5s  глубина %d  %.0f мс  (ход %d)  угадано ходов соперника %d из %d", req.Board, resp.Move, resp.Depth, resp.Ms, len(s.hist)/2, resp.PonderHits, resp.PonderStarts)
	writeJSON(w, 200, resp)
}

type lastMove struct {
	N        int     `json:"n"` // сколько ходов сделано с запуска
	Move     string  `json:"move"`
	Depth    int     `json:"depth"`
	Ms       float64 `json:"ms"`
	Nodes    uint64  `json:"nodes"`
	Score    *int    `json:"score,omitempty"`
	FromBook bool    `json:"fromBook"`
	Hits     int     `json:"hits"`
	Starts   int     `json:"starts"`
	At       string  `json:"at"`
}

type configJSON struct {
	Bot   string    `json:"bot"`
	Ms    int       `json:"ms"`
	Depth int       `json:"depth"`
	Last  *lastMove `json:"last,omitempty"`
	Error string    `json:"error,omitempty"`
}

// handleConfig: GET — текущие настройки и последний ход; POST {bot, ms, depth} — сменить бота «на лету»
// (старые боты и история партии сбрасываются, следующий ход придёт уже от нового).
func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodPost {
		var req configJSON
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, configJSON{Error: "bad json: " + err.Error()})
			return
		}
		if req.Ms < 0 || req.Ms > 120000 || req.Depth < 0 || req.Depth > 14 || (req.Ms == 0 && req.Depth == 0) {
			writeJSON(w, 200, configJSON{Error: "ms 0..120000, глубина 0..14 (хотя бы одно не ноль)"})
			return
		}
		spec := strings.TrimSpace(req.Bot)
		if spec == "" || spec == "human" {
			writeJSON(w, 200, configJSON{Error: "нужен бот (v1, v2, walker) с тегами"})
			return
		}
		d := req.Depth
		if req.Ms > 0 {
			d = 0
		}
		if _, err := match.BotFactory(spec, d, req.Ms, 20); err != nil { // проверяем до применения
			writeJSON(w, 200, configJSON{Error: err.Error()})
			return
		}
		s.mu.Lock()
		s.resetBots()
		s.defSpec, s.defMs, s.depth = spec, req.Ms, req.Depth
		s.mu.Unlock()
		log.Printf("панель: бот %q, %d мс, глубина %d", spec, req.Ms, req.Depth)
	}
	s.mu.Lock()
	out := configJSON{Bot: s.defSpec, Ms: s.defMs, Depth: s.depth}
	if s.last.N > 0 {
		l := s.last
		out.Last = &l
	}
	s.mu.Unlock()
	writeJSON(w, 200, out)
}

func (s *server) handleReset(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.mu.Lock()
	s.resetBots()
	s.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "адрес сервера")
	spec := flag.String("bot", "v2", "бот: walker | v1 | v2[:Name=Value,...] [@book=файл]")
	ms := flag.Int("ms", 1000, "время на ход, мс (0 = по глубине)")
	depth := flag.Int("depth", 4, "глубина, если -ms 0")
	ponder := flag.Bool("ponder", true, "думать на ходу соперника (прогревает таблицу; если соперник сыграл угаданный ход, следующий ход считается глубже)")
	flag.Parse()

	if *ponder && !strings.Contains(*spec, "@ponder") {
		*spec += "@ponder"
	}
	s := &server{defSpec: *spec, defMs: *ms, depth: *depth, bots: map[string]match.Bot{}}
	if _, err := s.bot(*spec, *depth, *ms); err != nil { // чтобы ошибка в спецификации вылезла сразу
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/move", s.handleMove)
	mux.HandleFunc("/reset", s.handleReset)
	mux.HandleFunc("/api/config", s.handleConfig)
	tags := http.NewServeMux() // каталог тегов нужен userscript'у на другом origin: добавляем CORS
	match.RegisterUI(tags)
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, r *http.Request) { cors(w); tags.ServeHTTP(w, r) })
	mux.HandleFunc("/picker.js", func(w http.ResponseWriter, r *http.Request) { tags.ServeHTTP(w, r) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(panelHTML)
	})
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		cors(w)
		writeJSON(w, 200, map[string]any{"ok": true, "bot": s.defSpec, "ms": s.defMs})
	})
	log.Printf("bridge: бот %q, %d мс на ход, панель управления: http://%s/", *spec, *ms, *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

