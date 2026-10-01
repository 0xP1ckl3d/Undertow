package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"

	"undertow/internal/control"
)

func runConsoleRelayCommand(ctx context.Context, output io.Writer, call consoleCaller, args []string) error {
	if args[0] == "topology" {
		if len(args) != 1 {
			return errors.New("use topology")
		}
		data, err := call(ctx, http.MethodGet, "/v1/status", nil)
		if err != nil {
			return err
		}
		var status struct {
			Agents []control.AgentInfo `json:"agents"`
		}
		if err := json.Unmarshal(data, &status); err != nil {
			return err
		}
		printTopology(output, status.Agents)
		return nil
	}
	if len(args) < 3 {
		return errors.New("select an agent, then use relay start [BIND], relay list, or relay stop [BIND]")
	}
	agentID := args[1]
	path := "/v1/agents/" + url.PathEscape(agentID) + "/relays"
	switch args[2] {
	case "list":
		if len(args) != 3 {
			return errors.New("use relay list")
		}
		data, err := call(ctx, http.MethodGet, "/v1/relays?agent_id="+url.QueryEscape(agentID), nil)
		if err != nil {
			return err
		}
		var relays []control.RelayInfo
		if err := json.Unmarshal(data, &relays); err != nil {
			return err
		}
		fmt.Fprintf(output, "Relay listeners (%d):\n", len(relays))
		for _, relay := range relays {
			fmt.Fprintf(output, "  %s\n", relay.Bind)
		}
		return nil
	case "start":
		if len(args) > 4 {
			return errors.New("use relay start [BIND]")
		}
		bind := "127.0.0.1:8443"
		if len(args) == 4 {
			bind = args[3]
		}
		data, err := call(ctx, http.MethodPost, path, map[string]string{"bind": bind})
		if err != nil {
			return err
		}
		var relay control.RelayInfo
		if err := json.Unmarshal(data, &relay); err != nil {
			return err
		}
		fmt.Fprintf(output, "Relay listening on %s for child agents.\n", relay.Bind)
		return nil
	case "stop":
		if len(args) > 4 {
			return errors.New("use relay stop [BIND]")
		}
		if len(args) == 4 {
			path += "?bind=" + url.QueryEscape(args[3])
		}
		if _, err := call(ctx, http.MethodDelete, path, nil); err != nil {
			return err
		}
		fmt.Fprintln(output, "Relay listener stopped.")
		return nil
	default:
		return errors.New("use relay start [BIND], relay list, or relay stop [BIND]")
	}
}

func printTopology(output io.Writer, agents []control.AgentInfo) {
	byID := make(map[string]control.AgentInfo, len(agents))
	children := make(map[string][]control.AgentInfo)
	for _, agent := range agents {
		byID[agent.ID] = agent
	}
	for _, agent := range agents {
		parent := agent.Via
		if parent != "" && byID[parent].ID == "" {
			parent = ""
		}
		children[parent] = append(children[parent], agent)
	}
	for parent := range children {
		sort.Slice(children[parent], func(i, j int) bool { return children[parent][i].Hostname < children[parent][j].Hostname })
	}
	fmt.Fprintf(output, "Agent topology (%d):\n", len(agents))
	visited := make(map[string]bool)
	var walk func(string, int)
	walk = func(parent string, depth int) {
		for _, agent := range children[parent] {
			if visited[agent.ID] {
				continue
			}
			visited[agent.ID] = true
			path := "direct"
			if agent.Via != "" {
				path = "via " + agentRouteLabel(agent.Via, agents)
			}
			for i := 0; i < depth; i++ {
				fmt.Fprint(output, "   ")
			}
			if depth > 0 {
				fmt.Fprint(output, "└─ ")
			}
			fmt.Fprintf(output, "%s (%s) %s [%s]\n", consoleAgentName(agent), shortAgentID(agent.ID), path, agent.Transport)
			walk(agent.ID, depth+1)
		}
	}
	walk("", 0)
}
