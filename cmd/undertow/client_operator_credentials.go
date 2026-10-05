//go:build linux || windows

package main

import (
	"errors"
	"fmt"
	"os"

	"undertow/internal/control"
)

const operatorIDEnv = "UNDERTOW_OPERATOR_ID"
const operatorPasswordEnv = "UNDERTOW_OPERATOR_PASSWORD"

// Resolve credentials before the terminal launcher forks its worker. A
// prompted password is passed to that worker in its private environment,
// never in command-line arguments or the local GUI.
func resolveClientOperatorCredentials(id, passwordFile string, allowPrompt ...bool) (control.OperatorCredentials, error) {
	canPrompt := len(allowPrompt) == 0 || allowPrompt[0]
	if id == "" {
		id = os.Getenv(operatorIDEnv)
	}
	if id == "" {
		if !canPrompt || !isConsoleTerminal(os.Stdin) {
			return control.OperatorCredentials{}, errors.New("operator ID required: use --operator or UNDERTOW_OPERATOR_ID")
		}
		fmt.Fprint(os.Stderr, "Operator ID: ")
		if _, err := fmt.Fscanln(os.Stdin, &id); err != nil {
			return control.OperatorCredentials{}, err
		}
	}
	if passwordFile != "" && os.Getenv(operatorPasswordEnv) != "" {
		return control.OperatorCredentials{}, errors.New("choose --operator-password-file or UNDERTOW_OPERATOR_PASSWORD")
	}
	password := ""
	if passwordFile != "" {
		value, err := operatorPasswordFromFile(passwordFile)
		if err != nil {
			return control.OperatorCredentials{}, err
		}
		password = value
	} else if value, ok := os.LookupEnv(operatorPasswordEnv); ok {
		password = value
	} else {
		if !canPrompt || !isConsoleTerminal(os.Stdin) {
			return control.OperatorCredentials{}, errors.New("operator password required: use --operator-password-file, UNDERTOW_OPERATOR_PASSWORD, or start in a terminal")
		}
		value, err := promptHiddenPassword("Operator password: ")
		if err != nil {
			return control.OperatorCredentials{}, err
		}
		password = value
	}
	if id == "" || len(password) < 12 || len(password) > 72 {
		return control.OperatorCredentials{}, errors.New("operator ID and a 12-72 byte password are required")
	}
	return control.OperatorCredentials{ID: id, Password: password}, nil
}

func promptHiddenPassword(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	restore, err := setConsoleRaw(os.Stdin)
	if err != nil {
		return "", err
	}
	defer restore()
	defer fmt.Fprintln(os.Stderr)
	var password []byte
	var one [1]byte
	for {
		n, err := os.Stdin.Read(one[:])
		if err != nil {
			return "", err
		}
		if n == 0 {
			continue
		}
		switch one[0] {
		case '\r', '\n':
			return string(password), nil
		case 3:
			return "", errors.New("operator password prompt cancelled")
		case 8, 127:
			if len(password) > 0 {
				password = password[:len(password)-1]
			}
		default:
			if len(password) >= 72 {
				return "", errors.New("operator password exceeds 72 bytes")
			}
			password = append(password, one[0])
		}
	}
}
