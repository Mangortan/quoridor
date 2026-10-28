package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"quoridor/match"
)

func post(t *testing.T, h http.Handler, path string, body any) (int, stateJSON, map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var s stateJSON
	var e map[string]string
	if w.Code == 200 {
		json.Unmarshal(w.Body.Bytes(), &s)
	} else {
		json.Unmarshal(w.Body.Bytes(), &e)
	}
	return w.Code, s, e
}

func TestPlayFullGameOverHTTP(t *testing.T) {
	srv := &server{}
	h := srv.handler()
	code, s, _ := post(t, h, "/api/new", newReq{P0: "v2", P1: "walker", Depth: 2, Opening: 2, Seed: 1})
	if code != 200 || s.Plies != 2 {
		t.Fatalf("new: code %d plies %d", code, s.Plies)
	}
	for i := 0; i < 400 && s.Winner < 0 && !s.Draw; i++ {
		code, s, _ = post(t, h, "/api/step", struct{}{})
		if code != 200 {
			t.Fatalf("step code %d", code)
		}
	}
	if s.Winner < 0 && !s.Draw {
		t.Fatal("game did not finish")
	}
	if len(s.History) != s.Plies || s.Last == nil {
		t.Fatalf("history %d plies %d last %v", len(s.History), s.Plies, s.Last)
	}
	// после конца партии шаг ничего не меняет
	_, s2, _ := post(t, h, "/api/step", struct{}{})
	if s2.Plies != s.Plies {
		t.Fatal("step after the end must be a no-op")
	}
}

func TestBadRequests(t *testing.T) {
	h := (&server{}).handler()
	for _, r := range []newReq{{P0: "nope"}, {P0: "v2:Flex=x"}, {Depth: 99}, {Opening: 99}} {
		if code, _, e := post(t, h, "/api/new", r); code != 400 || e["error"] == "" {
			t.Errorf("%+v: code %d err %v", r, code, e)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/step", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/step: %d", w.Code)
	}
}

func TestIndexServed(t *testing.T) {
	h := (&server{}).handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("Quoridor Arena")) {
		t.Fatalf("index: %d", w.Code)
	}
}

func TestHumanVsBot(t *testing.T) {
	h := (&server{}).handler()
	code, s, _ := post(t, h, "/api/new", newReq{P0: "human", P1: "v2", Depth: 2})
	if code != 200 || !s.Humans[0] || s.Humans[1] || s.Legal == nil || len(s.Legal.Pawn) != 3 {
		t.Fatalf("new human game: code %d humans %v legal %+v", code, s.Humans, s.Legal)
	}
	// шаг бота, пока ходит человек, ничего не делает
	_, s2, _ := post(t, h, "/api/step", struct{}{})
	if s2.Plies != 0 {
		t.Fatal("bot must not move on human's turn")
	}
	// нелегальные ходы
	for _, bad := range []moveReq{{"pawn", 8, 8}, {"pawn", 5, 5}, {"h", 9, 0}, {"x", 0, 0}} {
		if code, _, e := post(t, h, "/api/move", bad); code != 400 || e["error"] == "" {
			t.Errorf("%+v must be rejected, got %d", bad, code)
		}
	}
	// ход пешкой, затем ход бота
	if code, s, _ = post(t, h, "/api/move", moveReq{"pawn", 1, 4}); code != 200 || s.Plies != 1 || s.Turn != 1 {
		t.Fatalf("human move: code %d plies %d turn %d", code, s.Plies, s.Turn)
	}
	if code, _, _ = post(t, h, "/api/move", moveReq{"pawn", 2, 4}); code != 400 {
		t.Fatal("human must not move on bot's turn")
	}
	_, s, _ = post(t, h, "/api/step", struct{}{})
	if s.Plies != 2 || s.Turn != 0 || s.Legal == nil {
		t.Fatalf("after bot move: plies %d turn %d", s.Plies, s.Turn)
	}
	// стена человека: любая легальная из списка принимается
	w := s.Legal.Walls[0]
	if code, s, _ = post(t, h, "/api/move", moveReq{w.K, w.R, w.C}); code != 200 || s.Walls[0] != 9 {
		t.Fatalf("wall move: code %d walls %v", code, s.Walls)
	}
}

func TestUndo(t *testing.T) {
	h := (&server{}).handler()
	post(t, h, "/api/new", newReq{P0: "human", P1: "walker"})
	post(t, h, "/api/move", moveReq{"pawn", 1, 4})
	_, s, _ := post(t, h, "/api/step", struct{}{})
	post(t, h, "/api/move", moveReq{"pawn", 2, 4})
	_, s, _ = post(t, h, "/api/step", struct{}{})
	if s.Plies != 4 {
		t.Fatalf("plies %d", s.Plies)
	}
	_, s, _ = post(t, h, "/api/undo", struct{}{})
	if s.Plies != 2 || s.Turn != 0 || s.Pawns[0] != [2]int{1, 4} {
		t.Fatalf("after undo: plies %d turn %d pawn0 %v", s.Plies, s.Turn, s.Pawns[0])
	}
	post(t, h, "/api/undo", struct{}{})
	_, s, _ = post(t, h, "/api/undo", struct{}{}) // нечего отменять
	if s.Plies != 0 {
		t.Fatalf("plies %d", s.Plies)
	}
}

func TestRepetitionDraw(t *testing.T) {
	h := (&server{}).handler()
	post(t, h, "/api/new", newReq{P0: "human", P1: "human"})
	var s stateJSON
	// оба качаются туда-обратно: позиция повторится трижды
	seq := []moveReq{{"pawn", 1, 4}, {"pawn", 7, 4}, {"pawn", 0, 4}, {"pawn", 8, 4}}
	for i := 0; i < 8 && !s.Draw; i++ {
		_, s, _ = post(t, h, "/api/move", seq[i%4])
	}
	if !s.Draw || s.DrawReason != "троекратное повторение позиции" {
		t.Fatalf("expected repetition draw: %+v", s.DrawReason)
	}
}

func TestHumanGameIsSaved(t *testing.T) {
	dir := t.TempDir()
	srv := &server{dir: dir}
	h := srv.handler()
	_, s, _ := post(t, h, "/api/new", newReq{P0: "human", P1: "walker"})
	for i := 0; i < 300 && s.Winner < 0 && !s.Draw; i++ {
		if s.Turn == 0 && s.Legal != nil {
			// человек просто идёт по кратчайшему пути: берём пешечный ход с наименьшей строкой... вниз
			best := s.Legal.Pawn[0]
			for _, c := range s.Legal.Pawn {
				if c[0] > best[0] {
					best = c
				}
			}
			_, s, _ = post(t, h, "/api/move", moveReq{"pawn", best[0], best[1]})
		} else {
			_, s, _ = post(t, h, "/api/step", struct{}{})
		}
	}
	if s.Winner < 0 && !s.Draw {
		t.Fatal("game did not finish")
	}
	data, err := os.ReadFile(filepath.Join(dir, "human_games.txt"))
	if err != nil {
		t.Fatal(err)
	}
	recs, err := match.ReadGames(bytes.NewReader(data))
	if err != nil || len(recs) != 1 || recs[0].ASide != 0 || recs[0].Winner != s.Winner {
		t.Fatalf("saved records: %v err %v data %q", recs, err, data)
	}
	if _, err := match.Replay(recs[0]); err != nil {
		t.Fatal(err)
	}
	// повторный шаг после конца не дописывает партию второй раз
	post(t, h, "/api/step", struct{}{})
	data2, _ := os.ReadFile(filepath.Join(dir, "human_games.txt"))
	if !bytes.Equal(data, data2) {
		t.Fatal("game saved twice")
	}
}

