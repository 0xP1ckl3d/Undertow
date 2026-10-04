package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"undertow/internal/control"
)

func runConsoleTransportCommand(ctx context.Context, output io.Writer, call consoleCaller, args []string) error {
	if len(args) == 1 && args[0] == "transports" {
		data, err := call(ctx, http.MethodGet, "/v1/transports", nil)
		if err != nil {
			return err
		}
		var listeners []control.ListenerInfo
		if err := json.Unmarshal(data, &listeners); err != nil {
			return err
		}
		fmt.Fprintf(output, "%-12s %-8s %-22s %-14s %8s\n", "Transport", "Network", "Listen", "TLS", "Sessions")
		for _, listener := range listeners {
			tlsMode := listener.TLSMode
			if tlsMode == "" {
				tlsMode = "-"
			}
			fmt.Fprintf(output, "%-12s %-8s %-22s %-14s %8d\n", strings.ToUpper(listener.Transport), strings.ToUpper(listener.Network), listener.Listen, tlsMode, listener.Sessions)
		}
		return nil
	}
	if len(args) < 3 || args[1] != "transport" {
		return errors.New("use transports, start transport NAME [...], or stop transport NAME [force]")
	}
	name, err := canonicalTransport(args[2])
	if err != nil {
		return err
	}
	path := "/v1/transports/" + url.PathEscape(name)
	if args[0] == "stop" {
		if len(args) > 4 || len(args) == 4 && !strings.EqualFold(args[3], "force") {
			return errors.New("use stop transport NAME [force]")
		}
		if len(args) == 4 {
			path += "?force=true"
		}
		if _, err := call(ctx, http.MethodDelete, path, nil); err != nil {
			return err
		}
		fmt.Fprintf(output, "%s listener stopped.\n", strings.ToUpper(name))
		return nil
	}
	if args[0] != "start" {
		return errors.New("use start transport NAME [self-signed|tls-cert FILE tls-key FILE] [listen ADDR]")
	}
	var request control.TransportStartRequest
	for i := 3; i < len(args); {
		switch strings.ToLower(args[i]) {
		case "self-signed":
			request.TLSMode = "self-signed"
			i++
		case "listen":
			if i+1 >= len(args) {
				return errors.New("listen requires an address")
			}
			request.Listen = args[i+1]
			i += 2
		case "tls-cert":
			if i+1 >= len(args) {
				return errors.New("tls-cert requires a file")
			}
			request.TLSCert = args[i+1]
			i += 2
		case "tls-key":
			if i+1 >= len(args) {
				return errors.New("tls-key requires a file")
			}
			request.TLSKey = args[i+1]
			i += 2
		default:
			return fmt.Errorf("unknown transport option %q", args[i])
		}
	}
	data, err := call(ctx, http.MethodPost, path, request)
	if err != nil {
		return err
	}
	var info control.ListenerInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return err
	}
	fmt.Fprintf(output, "%s listening on %s (%s, TLS %s).\n", strings.ToUpper(info.Transport), info.Listen, info.Network, info.TLSMode)
	return nil
}

// A client console follows the same local safety rule as the browser service:
// it cannot stop the server carrier carrying its own control session.
func checkClientCarrierStop(ctx context.Context, call consoleCaller, ownClientID uint64, args []string) error {
	if ownClientID == 0 || len(args) < 3 || args[1] != "transport" {
		return nil
	}
	name, err := canonicalTransport(args[2])
	if err != nil {
		return err
	}
	data, err := call(ctx, http.MethodGet, "/v1/status", nil)
	if err != nil {
		return err
	}
	var status struct {
		Clients []control.ClientInfo `json:"clients"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return err
	}
	for _, client := range status.Clients {
		if client.SessionID == ownClientID && client.Transport == name {
			return errors.New("switch this client to another carrier before stopping its current carrier")
		}
	}
	return nil
}
