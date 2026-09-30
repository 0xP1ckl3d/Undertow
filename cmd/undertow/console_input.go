//go:build linux || windows

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

type consoleEditor struct {
	mu             sync.Mutex
	output         io.Writer
	prompt         string
	selected       bool
	vpn            bool
	serverAttached bool
	line           []rune
	cursor         int
	history        []string
	historyAt      int
	rawInput       chan byte
	rawDetach      chan struct{}
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
	fmt.Fprintf(e.output, "\r\x1b[2K\n[%s]\n", message)
	e.redraw()
}

func (e *consoleEditor) redraw() {
	fmt.Fprintf(e.output, "\r\x1b[2K%s%s", e.prompt, string(e.line))
	if trailing := len(e.line) - e.cursor; trailing > 0 {
		fmt.Fprintf(e.output, "\x1b[%dD", trailing)
	}
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
			if char == '\r' {
				char = '\n'
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
			e.line, e.cursor = nil, 0
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
			first, _, readErr := reader.ReadRune()
			if readErr == nil && first == '[' {
				key, _, readErr := reader.ReadRune()
				if readErr == nil {
					switch key {
					case 'A':
						e.recall(-1)
					case 'B':
						e.recall(1)
					case 'C':
						if e.cursor < len(e.line) {
							e.cursor++
						}
					case 'D':
						if e.cursor > 0 {
							e.cursor--
						}
					}
				}
			}
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

func (e *consoleEditor) complete() {
	input := string(e.line)
	if strings.ContainsAny(input, " \t") || e.cursor != len(e.line) {
		return
	}
	commands := []string{"agents", "use", "agent", "status", "routes", "jobs", "job", "help", "quit", "exec", "shell", "run-script", "run-wasm", "pwd", "ls", "stat", "mkdir", "rm", "whoami", "ps", "privileges", "env", "interfaces", "dns", "route-table"}
	if e.selected {
		commands = []string{"show", "exec", "shell", "run-script", "run-wasm", "jobs", "job", "routes", "route", "status", "back", "help", "quit", "pwd", "ls", "stat", "mkdir", "rm", "whoami", "ps", "privileges", "env", "interfaces", "dns", "route-table"}
		if e.vpn {
			commands = append(commands, "upload", "download")
		}
	}
	if e.vpn {
		commands = append(commands, "background", "internal", "forward")
	} else if e.serverAttached {
		commands = append(commands, "background", "logs", "stop")
	}
	var matches []string
	for _, command := range commands {
		if strings.HasPrefix(command, input) {
			matches = append(matches, command)
		}
	}
	if len(matches) == 0 {
		return
	}
	common := matches[0]
	for _, match := range matches[1:] {
		for !strings.HasPrefix(match, common) {
			common = common[:len(common)-1]
		}
	}
	if common != input {
		e.line = []rune(common)
		e.cursor = len(e.line)
	} else if len(matches) > 1 {
		fmt.Fprintf(e.output, "\r\n%s\r\n", strings.Join(matches, "  "))
	} else {
		e.line = append(e.line, ' ')
		e.cursor++
	}
}
