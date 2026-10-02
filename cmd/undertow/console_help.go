package main

import (
	"fmt"
	"io"
	"strings"
)

func printConsoleHelp(output io.Writer, vpnClient, selected, serverAttached bool, topic string, options ...consoleHelpOptions) error {
	topic = strings.ToLower(topic)
	if topic != "" && len(options) > 0 && options[0].color {
		var plain strings.Builder
		plainOptions := options[0]
		plainOptions.color = false
		if err := printConsoleHelp(&plain, vpnClient, selected, serverAttached, topic, plainOptions); err != nil {
			return err
		}
		printColorConsoleTopic(output, plain.String())
		return nil
	}
	if topic == "" {
		var opt consoleHelpOptions
		if len(options) > 0 {
			opt = options[0]
		}
		printConsoleOverview(output, vpnClient, selected, serverAttached, opt)
		return nil
	}
	if len(options) > 0 {
		if entry := options[0].loadedBOFs.get(topic); entry != nil {
			printLoadedBOFHelp(output, entry)
			return nil
		}
	}
	switch topic {
	case "payload", "profile", "artifact", "artifacts", "retrieval-path":
		printPayloadWorkflow(output)
		fmt.Fprint(output, `
Profile fields: server, transport, domain, fingerprint, auth, token-file,
password-file, payload-profile, websocket-path, tls-server-name,
tls-insecure-skip-verify, deny, routes (comma-separated IPv4 CIDRs).
If a listener binds 0.0.0.0 or ::, set server= to its reachable address.
The server operator manages payloads; a VPN client needs a distribution-admin
grant for changes. Profile and payload views never show enrollment secrets.
`)
	case "agent":
		fmt.Fprint(output, `Connected agents:
  agents                         List live agents and their agent IDs.
  use NUMBER|AGENT_ID|HOSTNAME    Select one agent.
  show                           Inspect the selected agent.
  agent events AGENT_ID          Read server-side lifecycle events.
  agent shutdown AGENT_ID        Ask a running packaged agent to exit.
  session kill AGENT_ID          Close only its session; it may reconnect.
For deployment binaries, use payload and type help payload for the workflow.
`)
	case "load", "unload", "bofs":
		fmt.Fprint(output, `Loaded BOF commands (local to this console session):
  load bof FILE [NAME] [--format FORMAT]
  unload bof NAME
  bofs
Load validates the object and an optional FILE.o.json or FILE.json sidecar.
Without a sidecar or --format, the argument schema is unspecified and supplied
arguments are encoded as ANSI strings. Use --format or a sidecar for typed
arguments such as integers, wide strings, or binary data.
Select an agent and type the loaded command name to execute it.
`)
	case "help", "navigation", "agents", "use", "back", "show", "status":
		fmt.Fprint(output, `Navigation and inspection:
  agents                    List connected agents with current numbers.
  use NUMBER|ID|HOSTNAME    Select an agent; numbers can change on reconnect.
  show                      Show the selected agent's details.
  back                      Return to the main menu.
  status [--json]           Show listeners, sessions, routes and counters.
  help [TOPIC]              Show categories or detailed command help.
  clear / cls               Clear the interactive screen.
`)
	case "clear", "cls":
		fmt.Fprint(output, `Console display:
  clear                     Clear the screen in an interactive terminal.
  cls                       Alias for clear.
The worker and current agent selection continue unchanged.
`)
	case "route", "routes", "routing":
		if vpnClient {
			fmt.Fprint(output, `Client routes (installed on this client only):
  routes                    Show available and accepted routes.
  route accept CIDR         Accept a selected agent's advertised route.
  route add CIDR            Use a selected agent for a manual route.
  route del CIDR            Remove a locally saved route.
At the main menu, append AGENT_ID to route accept/add. --internal leaves
ordinary Internet routing unchanged; --vpn also routes Internet via server.
`)
		} else {
			fmt.Fprint(output, `Server routes (global operator configuration):
  routes                    List configured routes.
  route add CIDR            Add a route through the selected agent.
  route del CIDR            Remove a route.
At the main menu, use route add CIDR AGENT_ID. The server needs --tun only
when applications on the server host itself should use these routes.
`)
		}
	case "relay", "topology":
		if vpnClient {
			return fmt.Errorf("relay and topology are managed in the server console")
		}
		fmt.Fprint(output, `Agent relays (server console; enter each command separately):
  topology                  Show parent and child agent paths.
  agents                    Find the parent agent's number.
  use NUMBER                Select that parent agent.
  relay start [BIND]        Open a listener on that agent for child agents.
  relay list                Show its relay listeners.
  relay stop [BIND]         Close one listener (or the only listener).
Omitting BIND uses 127.0.0.1:8443. Use an agent-reachable address such as
10.20.1.15:8443 for a child on another host. The child connects with
agent --transport relay --server ADDRESS --fingerprint FINGERPRINT.
The parent must allow the relay capability; --deny=relay blocks it.
`)
	case "transport", "transports", "start", "stop":
		if vpnClient {
			return fmt.Errorf("transport listeners are managed in the server console")
		}
		fmt.Fprint(output, `Server transport listeners:
  transports                Show every listener, TLS mode and session count.
  start transport NAME [self-signed|tls-cert FILE tls-key FILE] [listen ADDR]
  stop transport NAME       Close an idle listener.
  stop transport NAME force Disconnect its sessions, then close it.
NAME is dns, websocket or quic. The server starts all three by default.
Bare stop gracefully shuts down the server worker.
`)
	case "shell", "interactive":
		fmt.Fprint(output, `Live agent shell (enter each command separately):
  agents                    Find the agent's number.
  use NUMBER                Select that agent.
  shell [PROGRAM ARGS]      Open its default shell or a specified program.
  Ctrl-]                   Detach from only this shell.
Linux uses a PTY; Windows uses ConPTY. The agent must allow interactive.
`)
	case "exec":
		fmt.Fprint(output, `One-shot agent execution:
  agents                    Find the agent's number.
  use NUMBER                Select that agent.
  exec PROGRAM [ARGS]       Run a program without an implicit shell.
At the main menu, use exec AGENT_ID PROGRAM [ARGS].
`)
	case "run-script", "script", "scripts":
		fmt.Fprint(output, `Run a local script on the selected agent:
  run-script [--background] bash|powershell LOCAL_FILE
The file is read from the console machine and streamed to the agent's
interpreter; it is not stored on the agent. Tab completes LOCAL_FILE paths.
At the main menu, put AGENT_ID immediately after run-script.
`)
	case "run-wasm", "wasm":
		fmt.Fprint(output, `Run a WASI module on the selected agent:
  run-wasm [--background] [--stdin LOCAL_FILE] MODULE_FILE [ARGS]
The module and optional stdin file are read from the console machine.
Tab completes both local paths. At the main menu, put AGENT_ID after run-wasm.
The agent must allow the wasm capability.
`)
	case "run-native", "native":
		fmt.Fprint(output, `Run a Windows x64 native module on the selected agent:
  run-native [--background] [--data LOCAL_FILE] MODULE_FILE [ARGS]
MODULE_FILE is a .module container built with tools/nativepack. --data passes
opaque binary bytes; ARGS are UTF-8. At the main menu, put AGENT_ID after
run-native. The agent must allow the native capability.
`)
	case "run-bof", "bof":
		fmt.Fprint(output, `Run a Windows AMD64 BOF object on the selected agent:
  run-bof [--background] [--format FORMAT] [--manifest FILE] OBJECT.o [--format FORMAT] [ARGS]
  bof inspect OBJECT.o      Inspect locally from the command line.
The BOF must export go. Format characters: i=int32, s=int16, z=ANSI string,
Z=wide string, b=binary (@FILE or base64:DATA). A sidecar OBJECT.o.json can
provide argument types instead. At the main menu, put AGENT_ID after run-bof.
The agent must allow the native capability. See docs/bof-compatibility.md.
`)
	case "job", "jobs":
		fmt.Fprint(output, `Agent background tasks:
  job start PROGRAM [ARGS]  Start a task on the selected agent.
  jobs                      List numbered tasks (selected agent or all visible).
  jobs NUMBER               Show one task from the last jobs list.
  job show NUMBER|ID        Show state, times and exit code.
  job output NUMBER|ID      Read retained output.
  job cancel NUMBER|ID      Stop a running task.
  job stop NUMBER|ID        Alias for job cancel.
jobs show|output|cancel|stop NUMBER|ID also work. Job IDs remain valid if list
numbers change. At the main menu, jobs AGENT_ID filters the list.
run-script, run-wasm, run-native and run-bof also accept --background.
`)
	case "host", "hostops", "pwd", "ls", "stat", "mkdir", "rm", "whoami", "ps", "privileges", "env", "interfaces", "dns", "route-table":
		fmt.Fprint(output, `Built-in agent host operations (select an agent first):
  pwd                       Show the agent working directory.
  ls [PATH]                 List files.
  stat PATH                 Show file details.
  mkdir PATH                Create one directory.
  rm PATH                   Remove one file or empty directory.
  whoami                    Show the agent process identity.
  ps                        List processes.
  privileges                Show current privileges.
  env [NAME]                Show the environment or one variable.
  interfaces                List network interfaces and addresses.
  dns                       Show DNS configuration.
  route-table               Show the host route table.
These run on the agent. At the main menu, put AGENT_ID after the command.
rm removes one file or empty directory; it is not recursive.
`)
	case "files", "upload", "download":
		if !vpnClient {
			return fmt.Errorf("file transfer is available in the VPN client console")
		}
		fmt.Fprint(output, `Transfer files to or from a selected agent:
  upload LOCAL REMOTE       Send a client file to the agent.
  download REMOTE LOCAL     Save an agent file on the client.
At the main menu, put AGENT_ID after upload or download. Local paths are
on this console machine; Tab completes them. Transfers verify SHA-256.
`)
	case "forward":
		if !vpnClient {
			return fmt.Errorf("agent-side forwards are managed in the VPN client console")
		}
		fmt.Fprint(output, `Expose a client TCP service on a selected agent:
  forward add BIND TARGET  Bind on the agent; target the client service.
  forward list             List active forwards.
  forward del BIND         Close one forward.
At the main menu, put AGENT_ID after add/list/del. For example:
forward add 0.0.0.0:8080 127.0.0.1:8080
`)
	case "internal":
		if !vpnClient {
			return fmt.Errorf("internal mode is managed in the VPN client console")
		}
		fmt.Fprint(output, `Client internal routing:
  internal status            Show the mode and active routes by agent name and ID.
  internal on               Use server configured agent routes for new flows.
  internal off              Stop using those routes for new flows.
Routes explicitly accepted or added by this client remain active in either
setting. The setting survives a carrier reconnect while this client runs.
Use routes to inspect accepted routes and route del CIDR to remove one.
`)
	case "vpn":
		if !vpnClient {
			return fmt.Errorf("VPN Internet routing is managed in the client console")
		}
		fmt.Fprint(output, `Client Internet egress:
  vpn status                Show Internet mode, carrier and verified public IP.
  vpn on                    Install the two Undertow IPv4 Internet routes.
  vpn off                   Remove only Undertow's Internet routes.
This does not stop the client or change accepted internal routes. vpn on
verifies public egress and rolls its routes back if verification fails.
The chosen mode survives a carrier reconnect while this client runs.
`)
	case "logs":
		if !serverAttached {
			return fmt.Errorf("logs require an attached server console")
		}
		fmt.Fprint(output, `Server worker logs:
  logs                     Show recent log lines.
  logs follow              Stream new lines; press Enter to return.
`)
	case "lifecycle", "background", "quit", "exit":
		if vpnClient {
			fmt.Fprint(output, `VPN client lifecycle:
  background               Detach console; VPN and routes keep running.
  client attach            Reopen the console.
  quit / exit              Stop the client and remove its active routes.
  client --stop             Stop a detached client gracefully.
`)
		} else if serverAttached {
			fmt.Fprint(output, `Server lifecycle:
  background / quit / exit  Detach console; server keeps running.
  server attach            Reopen the console.
  stop                     Gracefully stop the server worker.
`)
		} else {
			fmt.Fprint(output, "Direct server operator console: quit / exit closes this console.\n")
		}
	default:
		return fmt.Errorf("unknown help topic %q; type help for available topics", topic)
	}
	return nil
}
