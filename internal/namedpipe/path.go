package namedpipe

import (
	"errors"
	"strings"
)

const localPrefix = `\\.\pipe\`

func validName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return name != "." && name != ".."
}

func ValidateLocal(path string) error {
	if !strings.HasPrefix(strings.ToLower(path), strings.ToLower(localPrefix)) || !validName(path[len(localPrefix):]) {
		return errors.New(`named-pipe listener must be \\.\pipe\NAME (letters, digits, dot, underscore, or hyphen)`)
	}
	return nil
}

func ValidateRemote(path string) error {
	if !strings.HasPrefix(path, `\\`) {
		return errors.New(`SMB relay address must be \\HOST\pipe\NAME`)
	}
	parts := strings.Split(path[2:], `\`)
	if len(parts) != 3 || !strings.EqualFold(parts[1], "pipe") || !validName(parts[2]) || len(parts[0]) == 0 || len(parts[0]) > 253 {
		return errors.New(`SMB relay address must be \\HOST\pipe\NAME`)
	}
	for _, r := range parts[0] {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return errors.New("invalid SMB relay host")
	}
	return nil
}

func IsLocal(path string) bool {
	return strings.HasPrefix(strings.ToLower(path), strings.ToLower(localPrefix))
}
