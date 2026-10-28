package match

import (
	_ "embed"
	"encoding/json"
	"net/http"
)

// Tag описывает тег спецификации бота (v2@ponder@conn2 ...) для выпадающих списков в интерфейсах.
type Tag struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`              // flag | int | intopt | string
	Desc    string `json:"desc"`              // подсказка
	Min     int    `json:"min,omitempty"`     // для int/intopt
	Max     int    `json:"max,omitempty"`     // для int/intopt
	Default string `json:"default,omitempty"` // для string
}

// Tags — все теги, которые понимает BotFactory (при добавлении нового тега в spec.go допишите его сюда).
var Tags = []Tag{
	{Name: "ponder", Kind: "flag", Desc: "думать на ходу соперника"},
	{Name: "tm", Kind: "flag", Desc: "управление временем: заканчивать раньше при устойчивом ходе, дольше думать при смене хода"},
	{Name: "pvs", Kind: "flag", Desc: "поиск нулевым окном (PVS)"},
	{Name: "lmr", Kind: "intopt", Min: 2, Max: 30, Desc: "сокращение поздних стен (LMR); число — с какого по счёту хода (пусто = 3)"},
	{Name: "ext", Kind: "flag", Desc: "продлевать ветку после стены, сильно удлиняющей путь соперника"},
	{Name: "conn", Kind: "int", Min: 0, Max: 2, Desc: "достраивающие стены в поиске: касание края/стен не менее чем N точками (0 = выкл)"},
	{Name: "cr", Kind: "flag", Desc: "достраивающие стены (conn) искать на ход мельче"},
	{Name: "slack", Kind: "int", Min: 0, Max: 4, Desc: "стены на обходных путях длиной до кратчайший+N (0 = выкл)"},
	{Name: "smp", Kind: "int", Min: 1, Max: 16, Desc: "число потоков поиска (Lazy SMP)"},
	{Name: "nobook", Kind: "flag", Desc: "сервер: не подключать книгу дебютов автоматически"},
	{Name: "book", Kind: "string", Default: "book.txt", Desc: "книга дебютов из файла"},
}

// Bases — базовые боты.
var Bases = []string{"v2", "v1", "walker"}

//go:embed ui/picker.js
var pickerJS []byte

// RegisterUI добавляет в mux /picker.js (виджет выбора бота с тегами) и /api/tags (каталог тегов).
func RegisterUI(mux *http.ServeMux) {
	mux.HandleFunc("/picker.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(pickerJS)
	})
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]any{"tags": Tags, "bases": Bases})
	})
}

