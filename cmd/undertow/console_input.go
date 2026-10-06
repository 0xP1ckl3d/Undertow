//go:build linux || windows

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"unicode"
)

type consoleEditor struct {
	mu              sync.Mutex
	output          io.Writer
	prompt          string
	selected        bool
	vpn             bool
	serverAttached  bool
	loadedBOFs      *loadedBOFRegistry
	loadedArtifacts *loadedArtifactRegistry
	line            []rune
	cursor          int
	history         []string
	historyAt       int
	rawInput        chan byte
	rawDetach       chan struct{}
	columns         int // optional override for non-terminal output and tests
	drawn           bool
	drawnCursorRow  int
	drawnColumns    int
}

func (e *consoleEditor) beginInteractive() (<-chan byte, <-chan struct{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rawInput = make(chan byte, 4096)
	e.rawDetach = make(chan struct{})
	return e.rawInput, e.rawDetach
}

func (e *consoleEditor) endInteractive() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.rawDetach != nil {
		select {
		case <-e.rawDetach:
		default:
			close(e.rawDetach)
		}
	}
	e.rawInput, e.rawDetach = nil, nil
}

func newConsoleEditor(output io.Writer, vpn bool) *consoleEditor {
	return &consoleEditor{output: output, vpn: vpn}
}

func (e *consoleEditor) showPrompt(prompt string, selected bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.prompt, e.selected = prompt, selected
	e.redraw()
}

func (e *consoleEditor) notice(message string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clearPrompt()
	fmt.Fprintf(e.output, "[%s]\r\n", message)
	e.redraw()
}

func (e *consoleEditor) printAsync(message string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clearPrompt()
	fmt.Fprint(e.output, message)
	if message == "" || message[len(message)-1] != '\n' {
		fmt.Fprint(e.output, "\r\n")
	}
	e.redraw()
}

func (e *consoleEditor) clearPrompt() {
	if !e.drawn {
		return
	}
	if e.drawnCursorRow > 0 {
		fmt.Fprintf(e.output, "\x1b[%dA", e.drawnCursorRow)
	}
	fmt.Fprint(e.output, "\r\x1b[J")
	e.drawn = false
}

func (e *consoleEditor) displayColumns() int {
	columns := e.columns
	if columns <= 0 {
		columns = 80
		if file, ok := e.output.(*os.File); ok {
			if width, _ := consoleSize(file); width > 1 {
				columns = int(width)
			}
		}
	}
	return columns
}

func (e *consoleEditor) redraw() {
	e.clearPrompt()
	columns := e.displayColumns()
	// Wrap explicitly before the final column. This avoids terminals' pending-wrap
	// state and gives every cursor position a well-defined row and column.
	span := max(1, columns-1)
	visible := []rune(e.prompt + string(e.line))
	cursorOffset := len([]rune(e.prompt)) + e.cursor
	for i, char := range visible {
		if i > 0 && i%span == 0 {
			fmt.Fprint(e.output, "\r\n")
		}
		fmt.Fprint(e.output, string(char))
	}
	endRow := len(visible) / span
	endCol := len(visible) % span
	if len(visible) > 0 && endCol == 0 {
		// The final row starts only when another character is printed.
		endRow--
		endCol = span
	}
	cursorRow := cursorOffset / span
	cursorCol := cursorOffset % span
	if cursorOffset > 0 && cursorCol == 0 {
		cursorRow--
		cursorCol = span
	}
	if endRow > cursorRow {
		fmt.Fprintf(e.output, "\x1b[%dA", endRow-cursorRow)
	}
	fmt.Fprint(e.output, "\r")
	if cursorCol > 0 {
		fmt.Fprintf(e.output, "\x1b[%dC", cursorCol)
	}
	e.drawn = true
	e.drawnCursorRow = cursorRow
	e.drawnColumns = columns
}

func (e *consoleEditor) read(ctx context.Context, input io.Reader, lines chan<- string) error {
	reader := bufio.NewReader(input)
	for {
		char, _, err := reader.ReadRune()
		if err != nil {
			return err
		}
		e.mu.Lock()
		if e.rawInput != nil {
			inputChannel, detach := e.rawInput, e.rawDetach
			e.mu.Unlock()
			if char == 0x1d {
				select {
				case <-detach:
				default:
					close(detach)
				}
				continue
			}
			for _, b := range []byte(string(char)) {
				select {
				case inputChannel <- b:
				case <-detach:
				case <-ctx.Done():
					return nil
				}
			}
			continue
		}
		switch char {
		case '\r', '\n':
			line := string(e.line)
			if line != "" && (len(e.history) == 0 || e.history[len(e.history)-1] != line) {
				e.history = append(e.history, line)
			}
			e.historyAt = len(e.history)
			if e.cursor != len(e.line) {
				e.cursor = len(e.line)
				e.redraw()
			}
			e.line, e.cursor = nil, 0
			e.drawn = false
			e.drawnCursorRow = 0
			fmt.Fprint(e.output, "\r\n")
			e.mu.Unlock()
			select {
			case lines <- line:
			case <-ctx.Done():
				return nil
			}
			continue
		case 0x08, 0x7f:
			if e.cursor > 0 {
				e.line = append(e.line[:e.cursor-1], e.line[e.cursor:]...)
				e.cursor--
			}
		case '\t':
			e.complete()
		case 0x1b:
			e.readEscape(reader)
		case 0x04:
			if len(e.line) == 0 {
				e.mu.Unlock()
				return io.EOF
			}
		case 0x03:
			if !e.vpn {
				e.mu.Unlock()
				return io.EOF
			}
			fmt.Fprint(e.output, "\r\x1b[2KStop VPN and remove its routes? [y/N] ")
			e.mu.Unlock()
			answer, _, readErr := reader.ReadRune()
			if readErr != nil {
				return readErr
			}
			e.mu.Lock()
			fmt.Fprint(e.output, "\r\n")
			if answer == 'y' || answer == 'Y' {
				e.line, e.cursor = nil, 0
				e.mu.Unlock()
				select {
				case lines <- "quit":
				case <-ctx.Done():
				}
				return nil
			}
		default:
			if char >= ' ' {
				if e.cursor == len(e.line) && e.drawn && e.drawnColumns == e.displayColumns() {
					span := max(1, e.drawnColumns-1)
					if (len([]rune(e.prompt))+len(e.line))%span == 0 {
						fmt.Fprint(e.output, "\r\n")
						e.drawnCursorRow++
					}
					fmt.Fprint(e.output, string(char))
					e.line = append(e.line, char)
					e.cursor++
					e.mu.Unlock()
					continue
				}
				e.line = append(e.line, 0)
				copy(e.line[e.cursor+1:], e.line[e.cursor:])
				e.line[e.cursor] = char
				e.cursor++
			}
		}
		e.redraw()
		e.mu.Unlock()
	}
}

func (e *consoleEditor) recall(direction int) {
	if len(e.history) == 0 {
		return
	}
	next := e.historyAt + direction
	if next < 0 || next > len(e.history) {
		return
	}
	e.historyAt = next
	if next == len(e.history) {
		e.line = nil
	} else {
		e.line = []rune(e.history[next])
	}
	e.cursor = len(e.line)
}

func (e *consoleEditor) readEscape(reader *bufio.Reader) {
	first, _, err := reader.ReadRune()
	if err != nil {
		return
	}
	if first == 'O' {
		key, _, err := reader.ReadRune()
		if err == nil {
			e.handleEscape("", key)
		}
		return
	}
	if first != '[' {
		return
	}
	var params []rune
	for len(params) < 32 {
		key, _, err := reader.ReadRune()
		if err != nil {
			return
		}
		if key >= 0x40 && key <= 0x7e {
			e.handleEscape(string(params), key)
			return
		}
		params = append(params, key)
	}
}

func (e *consoleEditor) handleEscape(params string, key rune) {
	modified := params == "1;5" || params == "5"
	switch key {
	case 'A':
		e.recall(-1)
	case 'B':
		e.recall(1)
	case 'C':
		if modified {
			for e.cursor < len(e.line) && !unicode.IsSpace(e.line[e.cursor]) {
				e.cursor++
			}
			for e.cursor < len(e.line) && unicode.IsSpace(e.line[e.cursor]) {
				e.cursor++
			}
		} else if e.cursor < len(e.line) {
			e.cursor++
		}
	case 'D':
		if modified {
			for e.cursor > 0 && unicode.IsSpace(e.line[e.cursor-1]) {
				e.cursor--
			}
			for e.cursor > 0 && !unicode.IsSpace(e.line[e.cursor-1]) {
				e.cursor--
			}
		} else if e.cursor > 0 {
			e.cursor--
		}
	case 'H':
		e.cursor = 0
	case 'F':
		e.cursor = len(e.line)
	case '~':
		switch params {
		case "3":
			if e.cursor < len(e.line) {
				e.line = append(e.line[:e.cursor], e.line[e.cursor+1:]...)
			}
		case "1", "7":
			e.cursor = 0
		case "4", "8":
			e.cursor = len(e.line)
		}
	}
}
