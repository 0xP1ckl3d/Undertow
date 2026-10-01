//go:build linux || windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

type consoleCompletion struct {
	value     string
	directory bool
	path      bool
	quote     rune
}

func (e *consoleEditor) complete() {
	if e.cursor != len(e.line) {
		return
	}
	start, before, partial, quote := completionWords(e.line)
	matches := consoleCompletions(before, partial, quote, e.selected, e.vpn, e.serverAttached)
	if len(matches) == 0 {
		return
	}
	current := string(e.line[start:])
	common := matches[0].value
	for _, match := range matches[1:] {
		for !strings.HasPrefix(match.value, common) {
			common = common[:len(common)-1]
		}
	}
	if common != current {
		e.line = append(e.line[:start], []rune(common)...)
		if len(matches) == 1 && matches[0].path && !matches[0].directory {
			if matches[0].quote != 0 {
				e.line = append(e.line, matches[0].quote)
			}
			e.line = append(e.line, ' ')
		}
		e.cursor = len(e.line)
		return
	}
	if len(matches) > 1 {
		labels := make([]string, len(matches))
		for i, match := range matches {
			labels[i] = match.value
		}
		fmt.Fprintf(e.output, "\r\n%s\r\n", strings.Join(labels, "  "))
		return
	}
	match := matches[0]
	if match.path && match.directory {
		return
	}
	if match.path && match.quote != 0 {
		e.line = append(e.line, match.quote)
	}
	e.line = append(e.line, ' ')
	e.cursor = len(e.line)
}

// completionWords accepts an unfinished quoted argument. Unlike command
// parsing, Tab must work before the closing quote has been typed.
func completionWords(line []rune) (start int, before []string, partial string, openingQuote rune) {
	start = len(line)
	word := make([]rune, 0, len(line))
	var quote rune
	for i, char := range line {
		if start == len(line) {
			if unicode.IsSpace(char) {
				continue
			}
			start = i
			if char == '\'' || char == '"' {
				openingQuote = char
			}
		}
		switch {
		case quote != 0 && char == quote:
			quote = 0
		case quote != 0:
			word = append(word, char)
		case char == '\'' || char == '"':
			quote = char
		case unicode.IsSpace(char):
			before = append(before, string(word))
			word = word[:0]
			start = len(line)
			openingQuote = 0
		default:
			word = append(word, char)
		}
	}
	return start, before, string(word), openingQuote
}

func consoleCompletions(before []string, partial string, quote rune, selected, vpn, attached bool) []consoleCompletion {
	if len(before) == 0 {
		commands := []string{"agents", "use", "agent", "status", "routes", "jobs", "job", "help", "quit", "exec", "shell", "run-script", "run-wasm", "pwd", "ls", "stat", "mkdir", "rm", "whoami", "ps", "privileges", "env", "interfaces", "dns", "route-table"}
		if selected {
			commands = append(commands, "show", "back", "route")
		}
		if vpn {
			commands = append(commands, "background", "internal", "forward", "upload", "download")
		} else {
			commands = append(commands, "transports", "topology", "start", "stop", "relay")
			if attached {
				commands = append(commands, "background", "logs")
			}
		}
		return wordCompletions(commands, partial)
	}
	switch before[0] {
	case "help":
		if len(before) == 1 {
			topics := []string{"agents", "route", "shell", "exec", "run-script", "run-wasm", "jobs", "host", "lifecycle"}
			if vpn {
				topics = append(topics, "files", "forward", "internal")
			} else {
				topics = append(topics, "relay", "transport")
				if attached {
					topics = append(topics, "logs")
				}
			}
			return wordCompletions(topics, partial)
		}
	case "relay":
		if len(before) == 1 {
			return wordCompletions([]string{"start", "list", "stop"}, partial)
		}
	case "route":
		if len(before) == 1 {
			verbs := []string{"add", "del"}
			if vpn {
				verbs = append(verbs, "accept")
			}
			return wordCompletions(verbs, partial)
		}
	case "run-script":
		args := before[1:]
		if !selected {
			if len(args) == 0 {
				return nil // agent ID belongs here
			}
			args = args[1:]
		}
		if len(args) == 0 {
			return wordCompletions([]string{"--background", "bash", "powershell"}, partial)
		}
		if len(args) == 1 && args[0] == "--background" {
			return wordCompletions([]string{"bash", "powershell"}, partial)
		}
		if len(args) == 1 && (args[0] == "bash" || args[0] == "powershell") || len(args) == 2 && args[0] == "--background" && (args[1] == "bash" || args[1] == "powershell") {
			return localPathCompletions(partial, quote, false)
		}
	case "run-wasm":
		args := before[1:]
		if !selected {
			if len(args) == 0 {
				return nil // agent ID belongs here
			}
			args = args[1:]
		}
		if strings.HasPrefix(partial, "--") {
			return wordCompletions([]string{"--background", "--stdin"}, partial)
		}
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--background":
				continue
			case "--stdin":
				if i == len(args)-1 {
					return localPathCompletions(partial, quote, false)
				}
				i++ // stdin path already supplied
			default:
				return nil // module already supplied; remaining words are its arguments
			}
		}
		return localPathCompletions(partial, quote, false)
	case "upload":
		want := 1
		if !selected {
			want++ // agent ID precedes local source
		}
		if len(before) == want {
			return localPathCompletions(partial, quote, false)
		}
	case "download":
		want := 2
		if !selected {
			want++ // agent ID and remote source precede local target
		}
		if len(before) == want {
			return localPathCompletions(partial, quote, true)
		}
	}
	return nil
}

func wordCompletions(words []string, partial string) []consoleCompletion {
	var matches []consoleCompletion
	for _, word := range words {
		if strings.HasPrefix(word, partial) {
			matches = append(matches, consoleCompletion{value: word})
		}
	}
	return matches
}

func localPathCompletions(partial string, quote rune, directoriesOnly bool) []consoleCompletion {
	separator := strings.LastIndexAny(partial, `/\`)
	directory, stem := ".", partial
	if separator >= 0 {
		directory, stem = partial[:separator+1], partial[separator+1:]
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	var matches []consoleCompletion
	for _, entry := range entries {
		name := entry.Name()
		match := strings.HasPrefix(name, stem)
		if runtime.GOOS == "windows" {
			match = strings.HasPrefix(strings.ToLower(name), strings.ToLower(stem))
		}
		if !match {
			continue
		}
		path := filepath.Join(directory, name)
		isDir := entry.IsDir()
		if entry.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(path); err == nil {
				isDir = info.IsDir()
			}
		}
		if directoriesOnly && !isDir {
			continue
		}
		value := partial[:len(partial)-len(stem)] + name
		if isDir {
			if separator >= 0 {
				value += string(partial[separator])
			} else {
				value += string(os.PathSeparator)
			}
		}
		usedQuote := quote
		if usedQuote == 0 && strings.ContainsAny(value, " \t") {
			usedQuote = '"'
			if strings.ContainsRune(value, '"') {
				usedQuote = '\''
			}
		}
		if usedQuote != 0 && strings.ContainsRune(value, usedQuote) {
			continue // this console parser cannot escape an embedded quote
		}
		if usedQuote != 0 {
			value = string(usedQuote) + value
		}
		matches = append(matches, consoleCompletion{value: value, directory: isDir, path: true, quote: usedQuote})
	}
	return matches
}
