package match

import (
	"fmt"
	"os"
	"strings"

	"quoridor/board"
)

// BadList — список плохих дебютов (линий в формате книги). Линия считается плохой, если она начинается
// с записанной в списке (или с её зеркального отражения) за ту же сторону: продолжения плохого начала
// проверять заново не нужно.
type BadList struct {
	lines []badLine
}

type badLine struct {
	side  int
	moves []board.Move
}

func hasPrefix(moves, prefix []board.Move) bool {
	if len(moves) < len(prefix) {
		return false
	}
	for i, m := range prefix {
		if moves[i] != m {
			return false
		}
	}
	return true
}

// Bad: попадает ли линия под список. Сторона side: 0 или 1 (линии с side=* в списке не участвуют).
func (bl *BadList) Bad(side int, moves []board.Move) bool {
	if bl == nil {
		return false
	}
	for _, l := range bl.lines {
		if l.side == side && (hasPrefix(moves, l.moves) || hasPrefix(moves, MirrorLine(l.moves))) {
			return true
		}
	}
	return false
}

func (bl *BadList) Len() int {
	if bl == nil {
		return 0
	}
	return len(bl.lines)
}

// Add добавляет линию в список (в памяти).
func (bl *BadList) Add(side int, moves []board.Move) {
	bl.lines = append(bl.lines, badLine{side, moves})
}

// ReadBadList читает список из файла; отсутствие файла — пустой список.
func ReadBadList(path string) (*BadList, error) {
	bl := &BadList{}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return bl, nil
	}
	if err != nil {
		return nil, err
	}
	for n, raw := range strings.Split(string(data), "\n") {
		side, mv, ok, err := ParseBookLine(raw)
		if err != nil {
			return nil, fmt.Errorf("%s, строка %d: %v", path, n+1, err)
		}
		if ok && side >= 0 {
			bl.Add(side, mv)
		}
	}
	return bl, nil
}

// AppendBad дописывает линию в файл списка (если её там ещё нет) и в bl.
func (bl *BadList) AppendBad(path string, side int, moves []board.Move, note string) error {
	if bl.Bad(side, moves) {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line := FormatBookLine(side, moves)
	if note != "" {
		line += "  # " + note
	}
	if _, err := fmt.Fprintln(f, line); err != nil {
		return err
	}
	bl.Add(side, moves)
	return nil
}

