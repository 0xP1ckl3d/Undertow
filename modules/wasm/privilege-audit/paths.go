package main

import "strings"

// Windows searches executable prefixes at spaces in an unquoted command line.
// A writable parent and a service restart are separate prerequisites.
func unquotedPathProbes(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" || command[0] == '"' {
		return nil
	}
	lower := strings.ToLower(command)
	end := strings.Index(lower, ".exe")
	if end < 0 {
		return nil
	}
	executable := command[:end+4]
	var probes []string
	for i, char := range executable {
		if char == ' ' && i > 0 {
			prefix := strings.TrimSpace(executable[:i])
			if !strings.HasSuffix(strings.ToLower(prefix), ".exe") {
				probes = append(probes, prefix+".exe")
			}
		}
	}
	return probes
}

func parent(path string) string {
	if index := strings.LastIndex(path, `\`); index >= 0 {
		if index == 2 && len(path) > 1 && path[1] == ':' {
			return path[:index+1]
		}
		return path[:index]
	}
	return path
}

func compactACL(value string) string { return strings.Join(strings.Fields(value), " ") }
