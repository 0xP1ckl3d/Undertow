package main

import (
	"errors"
	"flag"
	"fmt"

	"undertow/internal/control"
)

// Bootstrap runs locally before the server starts and never exposes a
// first-user endpoint on any carrier or HTTP listener.
func operatorAccountsCommand(args []string) error {
	if len(args) == 0 || args[0] != "bootstrap" {
		return errors.New("usage: undertow operators bootstrap [--operations-db PATH] [--id ID] [--display-name NAME] [--password-file PATH]")
	}
	f := flag.NewFlagSet("operators bootstrap", flag.ContinueOnError)
	db := f.String("operations-db", "operations.db", "server operations database")
	id := f.String("id", "", "initial Team Leader account ID")
	display := f.String("display-name", "", "initial Team Leader display name")
	passwordFile := f.String("password-file", "", "file containing the operator password")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected bootstrap argument")
	}
	credentials, err := resolveClientOperatorCredentials(*id, *passwordFile)
	if err != nil {
		return err
	}
	if *display == "" {
		*display = credentials.ID
	}
	store, err := control.OpenOperationsStore(*db)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.BootstrapOperator(credentials.ID, *display, credentials.Password); err != nil {
		return err
	}
	fmt.Printf("Initial Team Leader %s created in %s\n", credentials.ID, *db)
	return nil
}
