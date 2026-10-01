package main

import (
	"fmt"
	"io"
	"strings"
)

type consoleHelpOptions struct {
	agent      string
	color      bool
	loadedBOFs *loadedBOFRegistry
}

type consoleMenu struct {
	out   io.Writer
	color bool
}

func (m consoleMenu) paint(code, value string) string {
	if !m.color {
		return value
	}
	return "\x1b[" + code + "m" + value + "\x1b[0m"
}

func (m consoleMenu) title(role, location string) {
	fmt.Fprintln(m.out)
	fmt.Fprintf(m.out, "%s  %s\n", m.paint("1;91", "UNDERTOW"), m.paint("1;37", role+" / "+location))
	fmt.Fprintln(m.out, m.paint("2;33", "────────────────────────────────────────────────────────────"))
}

func (m consoleMenu) section(name string) {
	fmt.Fprintf(m.out, "\n%s\n", m.paint("1;33", "◆ "+name))
}

func (m consoleMenu) row(command, description string) {
	fmt.Fprintf(m.out, "  %s  %s\n", m.paint("1;97", fmt.Sprintf("%-30s", command)), description)
}

func (m consoleMenu) hint(message string) {
	fmt.Fprintln(m.out, m.paint("2;37", message))
}

func printConsoleOverview(output io.Writer, vpnClient, selected, serverAttached bool, opt consoleHelpOptions) {
	m := consoleMenu{out: output, color: opt.color}
	role := "SERVER"
	if vpnClient {
		role = "VPN CLIENT"
	}
	location := "MAIN MENU"
	if selected {
		location = "AGENT"
		if opt.agent != "" {
			location += " " + opt.agent
		}
	}
	m.title(role, location)
	m.section("NAVIGATION")
	m.row("agents", "List connected agents")
	m.row("use NUMBER|ID|HOSTNAME", "Enter an agent's menu")
	if selected {
		m.row("show", "Inspect this agent")
		m.row("back", "Return to the main menu")
	}
	m.row("status [--json]", "Show listeners, peers, routes and counters")
	m.row("help [TOPIC]", "Show detailed command help")
	m.row("clear / cls", "Clear the screen")
	m.row("load bof FILE [NAME]", "Register a local BOF command for this session")
	m.row("unload bof NAME", "Remove a loaded BOF command")
	m.row("bofs", "List loaded BOF commands")
	m.section("AGENT DISTRIBUTION")
	m.row("agent profile create NAME", "Configure a reusable agent profile")
	m.row("agent profile list", "List configured profiles")
	m.row("agent build NAME OS ARCH", "Stamp a prebuilt thin agent")
	m.row("agent artifacts", "List generated artifacts")
	m.row("agent host ID", "Expose an artifact over HTTPS")

	if selected {
		m.section("AGENT SESSION")
		m.row("shell [PROGRAM ARGS]", "Live terminal; Ctrl-] returns here")
		m.row("exec PROGRAM [ARGS]", "Run one program")
		m.row("run-script [OPTIONS] FILE", "Run a local script on this agent")
		m.row("run-wasm [OPTIONS] MODULE", "Run a local WASM module")
		m.row("run-native [OPTIONS] MODULE", "Run a Windows native module")
		m.row("run-bof [OPTIONS] OBJECT.o", "Run a Windows AMD64 BOF")
		m.row("job start PROGRAM [ARGS]", "Start a background task")
		m.row("jobs", "List this agent's numbered tasks")
		m.row("jobs NUMBER", "Show one task")
		m.row("job show NUMBER|ID", "Show task state and exit code")
		m.row("job output NUMBER|ID", "Read retained task output")
		m.row("job cancel NUMBER|ID", "Stop a running task")
		m.row("job stop NUMBER|ID", "Stop a running task")

		m.section("AGENT HOST")
		m.row("pwd", "Working directory")
		m.row("ls [PATH]", "List files")
		m.row("stat PATH", "File details")
		m.row("mkdir PATH", "Create a directory")
		m.row("rm PATH", "Remove a file or empty directory")
		m.row("whoami", "Process identity")
		m.row("ps", "Processes")
		m.row("privileges", "Privileges")
		m.row("env [NAME]", "Environment variables")
		m.row("interfaces", "Network interfaces")
		m.row("dns", "DNS settings")
		m.row("route-table", "Host route table")
	}

	if vpnClient {
		m.section("CLIENT ROUTING")
		m.row("routes", "Show available and accepted routes")
		if selected {
			m.row("route accept CIDR", "Accept this agent's advertised network")
			m.row("route add CIDR", "Add a manual route through this agent")
			m.row("route del CIDR", "Remove an accepted route")
		}
		m.row("internal on|off|status", "Control server routes for new flows")
		m.row("vpn on|off|status", "Control Internet egress through Undertow")
		if selected {
			m.section("AGENT FILES AND SERVICES")
			m.row("upload LOCAL REMOTE", "Send a file to this agent")
			m.row("download REMOTE LOCAL", "Save a file from this agent")
			m.row("forward add BIND TARGET", "Expose a client service on this agent")
			m.row("forward list", "List this agent's forwards")
			m.row("forward del BIND", "Close one forward")
		}
	} else {
		m.section("SERVER ROUTING AND TOPOLOGY")
		m.row("routes", "List server managed routes")
		if selected {
			m.row("route add CIDR", "Route through this agent")
			m.row("route del CIDR", "Remove a server route")
			m.row("relay start [BIND]", "Open a child-agent relay here")
			m.row("relay list", "List this agent's relays")
			m.row("relay stop [BIND]", "Close a relay")
		}
		m.row("topology", "Show direct and relayed agent paths")
		m.section("SERVER LISTENERS")
		m.row("transports", "Show DNS, WebSocket and QUIC listeners")
		m.row("start transport NAME ...", "Add a listener")
		m.row("stop transport NAME [force]", "Close a listener")
		if serverAttached {
			m.row("logs", "Show recent server logs")
			m.row("logs follow", "Follow logs; Enter returns")
		}
	}

	m.section("LIFECYCLE")
	if vpnClient || serverAttached {
		m.row("background", "Detach; keep the worker running")
	}
	if vpnClient {
		m.row("quit / exit", "Stop the VPN and remove its routes")
		m.hint("Reattach later: undertow client attach")
	} else {
		m.row("quit / exit", "Detach from the server")
		if serverAttached {
			m.row("stop", "Gracefully stop the server")
			m.hint("Reattach later: undertow server attach")
		}
	}
	m.hint("Type help TOPIC for details. Tab completes commands and local paths.")
	if opt.loadedBOFs != nil {
		if names := opt.loadedBOFs.names(); len(names) > 0 {
			m.section("Loaded BOFs")
			for _, name := range names {
				entry := opt.loadedBOFs.get(name)
				description := strings.SplitN(entry.Manifest.Description, "\n", 2)[0]
				if description == "" {
					description = "Windows AMD64 BOF"
				}
				m.row(name, description)
			}
		}
	}
	fmt.Fprintln(output)
}

func printColorConsoleTopic(output io.Writer, plain string) {
	m := consoleMenu{out: output, color: true}
	for i, line := range strings.SplitAfter(plain, "\n") {
		if line == "" {
			continue
		}
		if i == 0 {
			fmt.Fprint(output, m.paint("1;91", strings.TrimSuffix(line, "\n")), "\n")
			continue
		}
		if strings.HasPrefix(line, "  ") {
			if split := strings.Index(line[2:], "  "); split >= 0 {
				end := 2 + split
				fmt.Fprint(output, "  ", m.paint("1;97", line[2:end]), line[end:])
				continue
			}
		}
		fmt.Fprint(output, line)
	}
}
