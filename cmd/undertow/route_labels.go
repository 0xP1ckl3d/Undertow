package main

import "undertow/internal/control"

func agentRouteLabel(id string, agents []control.AgentInfo) string {
	for _, agent := range agents {
		if agent.ID == id {
			name := consoleAgentName(agent)
			if name != "" && name != id {
				return name + " (" + id + ")"
			}
			break
		}
	}
	return id
}
