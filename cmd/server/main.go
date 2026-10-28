// Command server: веб-интерфейс для игры против бота и просмотра партий бот-против-бота.
//
//	go run ./cmd/server            # затем открыть http://127.0.0.1:8080
//	go run ./cmd/server -addr :8080  # доступ с других устройств в сети
//
// Любая сторона может быть человеком ("human") или ботом (walker | v1 | v2[:Name=Value,...]).
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"quoridor/board"
	"quoridor/match"
	"quoridor/movegen"
	"quoridor/path"
	"quoridor/search"
)

//go:embed static
var staticFS embed.FS

// ---- состояние партии ----

type game struct {
	mu       sync.Mutex
	b        board.Board
	bots     [2]match.Bot // nil = человек
	names    [2]string
	moves    []board.Move
	texts    []string
	info     []moveInfo
	poss     []uint64 // ключи всех позиций партии, включая текущую
	seen     map[uint64]int
	winner   int
	repDraw  bool
	maxPlies int
	saveDir  string // куда дописывать партии с участием человека (пусто = не сохранять)
	meta     string
	saved    bool
	bookDone bool
}

type moveInfo struct {
	Player int     `json:"player"`
	Human  bool    `json:"human,omitempty"`
	Score  *int    `json:"score,omitempty"` // оценка поиска с точки зрения ходившего
	Depth  int     `json:"depth,omitempty"`
	Nodes  uint64  `json:"nodes,omitempty"`
	Ms     float64 `json:"ms"`
}

type newReq struct {
	P0       string `json:"p0"`
	P1       string `json:"p1"`
	Depth    int    `json:"depth"`
	Ms       int    `json:"ms"`
	Opening  int    `json:"opening"` // случайных полуходов дебюта
	Seed     int64  `json:"seed"`
	MaxPlies int    `json:"maxPlies"`
}

type server struct {
	mu  sync.Mutex
	g   *game
	dir string // каталог для сохранения партий человека
	// bookPath — файл книги дебютов: боты играют по нему, если файл есть, а кнопка «В дебют» дописывает в него линии.
	bookPath string
}

var saveMu sync.Mutex

func (g *game) finished() bool { return g.winner >= 0 || g.repDraw || len(g.moves) >= g.maxPlies }

func (g *game) human(p int) bool { return g.bots[p] == nil }

func (g *game) reset() {
	g.b = board.New()
	g.moves, g.texts, g.info = nil, nil, nil
	g.winner, g.repDraw, g.saved = -1, false, false
	k := search.Hash(&g.b)
	g.poss = []uint64{k}
	g.seen = map[uint64]int{k: 1}
}

func (s *server) newGame(r newReq) (*game, error) {
	if r.P0 == "" {
		r.P0 = "v2"
	}
	if r.P1 == "" {
		r.P1 = "v1"
	}
	if r.Depth == 0 && r.Ms == 0 {
		r.Depth = 3
	}
	if r.Depth < 0 || r.Depth > 12 || r.Ms < 0 || r.Ms > 60000 {
		return nil, fmt.Errorf("depth must be 0..12 and ms 0..60000")
	}
	if r.MaxPlies <= 0 || r.MaxPlies > 1000 {
		r.MaxPlies = 300
	}
	if r.Opening < 0 || r.Opening > 20 {
		return nil, fmt.Errorf("opening must be 0..20")
	}
	depth := r.Depth
	if r.Ms > 0 {
		depth = 0
	}
	g := &game{maxPlies: r.MaxPlies, names: [2]string{r.P0, r.P1}, saveDir: s.dir,
		meta: fmt.Sprintf("p0=%s p1=%s depth=%d ms=%d", r.P0, r.P1, r.Depth, r.Ms)}
	g.reset()
	for i, spec := range []string{r.P0, r.P1} {
		if spec == "human" {
			g.names[i] = "человек"
			continue
		}
		noBook := strings.Contains(spec, "@nobook")
		spec = strings.ReplaceAll(spec, "@nobook", "")
		if _, err := os.Stat(s.bookPath); !noBook && s.bookPath != "" && err == nil && !strings.Contains(spec, "@book=") && !strings.HasPrefix(spec, "walker") {
			spec += "@book=" + s.bookPath
		}
		if (r.P0 == "human" || r.P1 == "human") && !strings.Contains(spec, "@ponder") {
			spec += "@ponder" // против человека бот думает и на его ходу
		}
		f, err := match.BotFactory(spec, depth, r.Ms, 20)
		if err != nil {
			return nil, err
		}
		g.bots[i] = f()
	}
	if r.Opening > 0 {
		seed := r.Seed
		if seed == 0 {
			seed = time.Now().UnixNano()
		}
		for _, m := range match.RandomOpening(randSrc(seed), r.Opening) {
			g.apply(m, moveInfo{}, "дебют")
		}
	}
	return g, nil
}

func moveText(m board.Move) string {
	switch m.Kind() {
	case board.KindPawn:
		return fmt.Sprintf("пешка → (%d,%d)", m.Idx()/9, m.Idx()%9)
	case board.KindH:
		return fmt.Sprintf("гориз. стена (%d,%d)", m.Idx()/8, m.Idx()%8)
	default:
		return fmt.Sprintf("вертик. стена (%d,%d)", m.Idx()/8, m.Idx()%8)
	}
}

// stopPonder останавливает «думалку» ботов (после конца партии и при замене партии).
func (g *game) stopPonder() {
	for _, b := range g.bots {
		if sp, ok := b.(interface{ StopPonder() }); ok {
			sp.StopPonder()
		}
	}
}

func (g *game) apply(m board.Move, in moveInfo, tag string) {
	in.Player = int(g.b.Turn)
	txt := moveText(m)
	if tag != "" {
		txt += " [" + tag + "]"
	}
	g.b = g.b.Apply(m)
	g.moves = append(g.moves, m)
	g.texts = append(g.texts, txt)
	g.info = append(g.info, in)
	k := search.Hash(&g.b)
	g.poss = append(g.poss, k)
	g.seen[k]++
	if g.seen[k] >= 3 {
		g.repDraw = true
	}
	if w := g.b.Winner(); w >= 0 {
		g.winner = w
	}
	if g.finished() {
		g.stopPonder()
	}
}

// save дописывает законченную партию с участием человека в games/human_games.txt.
// ASide = сторона человека (если человек один), поэтому match.LossPositions(партии, 1, N)
// даёт позиции, в которых бот проиграл вам. Затем партии можно скормить cmd/learn.
func (g *game) save() {
	if g.saveDir == "" || g.saved || !g.finished() || len(g.moves) < 4 || !(g.human(0) || g.human(1)) {
		return
	}
	g.saved = true
	side := 0
	if !g.human(0) {
		side = 1
	}
	rec := match.Record{Moves: g.moves, Winner: g.winner, ASide: side}
	saveMu.Lock()
	defer saveMu.Unlock()
	if err := os.MkdirAll(g.saveDir, 0o755); err != nil {
		log.Printf("save: %v", err)
		return
	}
	f, err := os.OpenFile(filepath.Join(g.saveDir, "human_games.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("save: %v", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "# %s %s\n%s\n", time.Now().Format("2006-01-02 15:04"), g.meta, rec.String())
}

// finish вызывается после каждого хода: сохраняет законченную партию и сообщает исход книге дебютов.
func (g *game) finish() {
	g.save()
	if g.finished() && !g.bookDone {
		g.bookDone = true
		for _, bot := range g.bots {
			if bb, ok := bot.(*match.BookBot); ok {
				bb.Finish(g.winner)
			}
		}
	}
}

func bookRejected(b match.Bot) bool {
	bb, ok := b.(*match.BookBot)
	return ok && bb.Rejected
}

func fromBook(b match.Bot) bool {
	bb, ok := b.(*match.BookBot)
	return ok && bb.FromBook
}

// step: один ход бота, чья очередь. Ничего не делает, если партия окончена или ходит человек.
func (g *game) step() bool {
	if g.finished() {
		return false
	}
	p := int(g.b.Turn)
	bot := g.bots[p]
	if bot == nil {
		return false
	}
	if h, ok := bot.(interface{ SetHistory([]uint64) }); ok {
		h.SetHistory(g.poss[:len(g.poss)-1])
	}
	t0 := time.Now()
	m := bot.Choose(g.b)
	in := moveInfo{Ms: float64(time.Since(t0).Microseconds()) / 1000}
	if sb := match.SearcherOf(bot); sb != nil && !fromBook(bot) {
		sc := sb.Last.Score
		in.Score, in.Depth, in.Nodes = &sc, sb.Last.Depth, sb.Last.Nodes
	}
	tag := ""
	if fromBook(bot) {
		tag = "книга"
	} else if bookRejected(bot) {
		tag = "ход книги отвергнут поиском"
	}
	g.apply(m, in, tag)
	g.finish()
	return true
}

type moveReq struct {
	K string `json:"k"` // pawn | h | v
	R int    `json:"r"`
	C int    `json:"c"`
}

func (r moveReq) toMove() (board.Move, error) {
	switch r.K {
	case "pawn":
		if r.R < 0 || r.R > 8 || r.C < 0 || r.C > 8 {
			return 0, fmt.Errorf("клетка вне поля")
		}
		return board.PawnMove(r.R*9 + r.C), nil
	case "h", "v":
		if r.R < 0 || r.R > 7 || r.C < 0 || r.C > 7 {
			return 0, fmt.Errorf("стена вне поля")
		}
		return board.WallMove(r.K == "h", r.R, r.C), nil
	}
	return 0, fmt.Errorf("неизвестный вид хода %q", r.K)
}

// humanMove: ход человека; проверяет очередь и легальность по полному генератору правил.
func (g *game) humanMove(r moveReq) error {
	if g.finished() {
		return fmt.Errorf("партия окончена")
	}
	if !g.human(int(g.b.Turn)) {
		return fmt.Errorf("сейчас ход бота")
	}
	m, err := r.toMove()
	if err != nil {
		return err
	}
	for _, x := range movegen.GenAll(&g.b, nil) {
		if x == m {
			g.apply(m, moveInfo{Human: true}, "")
			g.finish()
			return nil
		}
	}
	return fmt.Errorf("нелегальный ход")
}

// undo откатывает назад до предыдущего хода человека (вместе с ответом бота).
func (g *game) undo() error {
	if len(g.moves) == 0 {
		return fmt.Errorf("нечего отменять")
	}
	moves, texts, info := g.moves, g.texts, g.info
	n := len(moves)
	for n > 0 {
		n--
		if g.human(info[n].Player) && info[n].Human {
			break
		}
	}
	if !(n < len(moves) && info[n].Human) {
		return fmt.Errorf("нет ходов человека для отмены")
	}
	g.reset()
	for i := 0; i < n; i++ {
		g.apply(moves[i], info[i], "")
		g.texts[i] = texts[i]
	}
	return nil
}

// ---- JSON ----

type wallJSON struct {
	K    string `json:"k"`
	R, C int
}

type legalJSON struct {
	Pawn  [][2]int   `json:"pawn"`
	Walls []wallJSON `json:"walls"`
}

type stateJSON struct {
	Names      [2]string  `json:"names"`
	Humans     [2]bool    `json:"humans"`
	HW         [][2]int   `json:"hw"`
	VW         [][2]int   `json:"vw"`
	Pawns      [2][2]int  `json:"pawns"`
	Walls      [2]int     `json:"walls"`
	Turn       int        `json:"turn"`
	Winner     int        `json:"winner"`
	Plies      int        `json:"plies"`
	Draw       bool       `json:"draw"`
	DrawReason string     `json:"drawReason,omitempty"`
	Dist       [2]int     `json:"dist"`
	Last       *lastJSON  `json:"last"`
	History    []histJSON `json:"history"`
	MaxPlies   int        `json:"maxPlies"`
	Legal      *legalJSON `json:"legal,omitempty"` // только когда ходит человек
}

type lastJSON struct {
	Kind   string `json:"kind"` // pawn | h | v
	R, C   int
	Player int `json:"player"`
}

type histJSON struct {
	Text string `json:"text"`
	moveInfo
}

func (g *game) state() stateJSON {
	s := stateJSON{Names: g.names, Turn: int(g.b.Turn), Winner: g.winner, Plies: len(g.moves), MaxPlies: g.maxPlies,
		Walls: [2]int{int(g.b.Walls[0]), int(g.b.Walls[1])}, HW: [][2]int{}, VW: [][2]int{},
		Humans: [2]bool{g.human(0), g.human(1)}}
	for i := 0; i < 64; i++ {
		if g.b.HW>>uint(i)&1 != 0 {
			s.HW = append(s.HW, [2]int{i / 8, i % 8})
		}
		if g.b.VW>>uint(i)&1 != 0 {
			s.VW = append(s.VW, [2]int{i / 8, i % 8})
		}
	}
	for p := 0; p < 2; p++ {
		s.Pawns[p] = [2]int{int(g.b.P[p]) / 9, int(g.b.P[p]) % 9}
		s.Dist[p] = path.Len(&g.b, p)
	}
	if g.winner < 0 && g.finished() {
		s.Draw = true
		s.DrawReason = fmt.Sprintf("лимит ходов (%d)", g.maxPlies)
		if g.repDraw {
			s.DrawReason = "троекратное повторение позиции"
		}
	}
	if n := len(g.moves); n > 0 {
		m := g.moves[n-1]
		l := &lastJSON{Player: g.info[n-1].Player}
		switch m.Kind() {
		case board.KindPawn:
			l.Kind, l.R, l.C = "pawn", m.Idx()/9, m.Idx()%9
		case board.KindH:
			l.Kind, l.R, l.C = "h", m.Idx()/8, m.Idx()%8
		default:
			l.Kind, l.R, l.C = "v", m.Idx()/8, m.Idx()%8
		}
		s.Last = l
	}
	s.History = []histJSON{}
	for i := range g.moves {
		s.History = append(s.History, histJSON{Text: g.texts[i], moveInfo: g.info[i]})
	}
	if !g.finished() && g.human(int(g.b.Turn)) {
		lg := &legalJSON{Pawn: [][2]int{}, Walls: []wallJSON{}}
		for _, m := range movegen.GenAll(&g.b, nil) {
			switch m.Kind() {
			case board.KindPawn:
				lg.Pawn = append(lg.Pawn, [2]int{m.Idx() / 9, m.Idx() % 9})
			case board.KindH:
				lg.Walls = append(lg.Walls, wallJSON{"h", m.Idx() / 8, m.Idx() % 8})
			default:
				lg.Walls = append(lg.Walls, wallJSON{"v", m.Idx() / 8, m.Idx() % 8})
			}
		}
		s.Legal = lg
	}
	return s
}

// ---- HTTP ----

func (s *server) current() *game {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.g == nil {
		s.g, _ = s.newGame(newReq{})
	}
	return s.g
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	match.RegisterUI(mux)

	write := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	fail := func(w http.ResponseWriter, code int, err error) {
		write(w, code, map[string]string{"error": err.Error()})
	}
	// post оборачивает обработчик: только POST, JSON-тело ограничено, партия под замком.
	post := func(path string, onGame bool, f func(g *game, body json.RawMessage) error) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				fail(w, http.StatusMethodNotAllowed, fmt.Errorf("POST only"))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
			var raw json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
				raw = nil // пустое тело допустимо
			}
			if !onGame {
				if err := f(nil, raw); err != nil {
					fail(w, http.StatusBadRequest, err)
					return
				}
				g := s.current()
				g.mu.Lock()
				defer g.mu.Unlock()
				write(w, 200, g.state())
				return
			}
			g := s.current()
			g.mu.Lock()
			defer g.mu.Unlock()
			if err := f(g, raw); err != nil {
				fail(w, http.StatusBadRequest, err)
				return
			}
			write(w, 200, g.state())
		})
	}

	post("/api/new", false, func(_ *game, raw json.RawMessage) error {
		var req newReq
		if err := json.Unmarshal(raw, &req); err != nil {
			return err
		}
		g, err := s.newGame(req)
		if err != nil {
			return err
		}
		s.mu.Lock()
		old := s.g
		s.g = g
		s.mu.Unlock()
		if old != nil { // «думалка» прежней партии больше не нужна
			old.mu.Lock()
			old.stopPonder()
			old.mu.Unlock()
		}
		return nil
	})
	post("/api/step", true, func(g *game, _ json.RawMessage) error { g.step(); return nil })
	post("/api/move", true, func(g *game, raw json.RawMessage) error {
		var req moveReq
		if err := json.Unmarshal(raw, &req); err != nil {
			return err
		}
		return g.humanMove(req)
	})
	// «В дебют»: записать текущую линию (ходы с начала партии) в книгу; брать будут ходы вашей стороны.
	post("/api/book", true, func(g *game, _ json.RawMessage) error { return s.addToBook(g) })
	post("/api/undo", true, func(g *game, _ json.RawMessage) error { return g.undo() })
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		g := s.current()
		g.mu.Lock()
		defer g.mu.Unlock()
		write(w, 200, g.state())
	})
	return mux
}

// addToBook дописывает в книгу линию текущей партии: ходы стороны человека (он должен быть один).
func (s *server) addToBook(g *game) error {
	if s.bookPath == "" {
		return fmt.Errorf("книга отключена (флаг -book пуст)")
	}
	if g.human(0) == g.human(1) {
		return fmt.Errorf("записывать в книгу можно, когда человек играет против бота")
	}
	side := 0
	if !g.human(0) {
		side = 1
	}
	if len(g.moves) <= side { // у человека ещё нет ни одного хода
		return fmt.Errorf("сначала сделайте хотя бы один ход")
	}
	if err := match.NewBook().AddLine(g.moves, side, 0); err != nil {
		return err
	}
	if bad, err := match.ReadBadList(s.bookPath + ".bad"); err == nil && bad.Bad(side, g.moves) {
		return fmt.Errorf("эта линия начинается с дебюта из списка плохих (%s.bad)", s.bookPath)
	}
	saveMu.Lock()
	defer saveMu.Unlock()
	f, err := os.OpenFile(s.bookPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s   # %s\n", match.FormatBookLine(side, g.moves), time.Now().Format("2006-01-02 15:04"))
	return err
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "адрес сервера (для доступа из сети: :8080)")
	dir := flag.String("games", "games", "каталог, куда сохраняются партии с вашим участием (пусто = не сохранять)")
	book := flag.String("book", "book.txt", "файл книги дебютов (боты играют по нему, если он есть; кнопка «В дебют» дописывает линии; пусто = выключить)")
	flag.Parse()
	s := &server{dir: *dir, bookPath: *book}
	s.current()
	log.Printf("Quoridor: http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, s.handler()))
}

