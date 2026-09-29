package main

import (
	"fmt"
	"io"
	"strings"
)

func helpRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func writeHelp(w io.Writer, topic string) error {
	topic = strings.ToLower(topic)
	var body string
	switch topic {
	case "":
		body = `Undertow — authenticated direct-DNS pivot and IPv4 VPN

Roles:
  server   Receives DNS/UDP sessions. Optional proxy TUN for internal routes.
  agent    Unprivileged process on an internal host; opens target sockets.
  client   Privileged process on a separate host; routes its IPv4 traffic
           through server Internet egress, optionally through agent routes.

An agent exposes destinations reachable from its host, without changing that
host's routes. A VPN client changes its own host's IPv4 routes. A VPN client
does not expose its local network; an agent does not provide VPN Internet
egress to its own host. For VPN access to agent networks, configure a server
route to the agent and run the VPN client with --internal.

Setup:
  1. On the server: undertow init
  2. Choose enrollment: token (default), password, or open (--auth none).
     Pin the server fingerprint or opt in to --trust-on-first-use.
  3. Start: undertow server --listen 0.0.0.0:53
  4. Start an agent or VPN client using --server and matching --auth options.

Commands:
  init       Create server identity and enrollment token.
  server     Run the DNS listener and operator control API.
  agent      Connect an internal host without changing its routes.
  client     Run the privileged IPv4 VPN; 'client attach' opens its console.
  console    Open an interactive server operator console.
  status     Show connected agents, VPN clients, and routes.
  route      Add, remove, or list agent pivot routes.
  session    Disconnect an agent session.
  version    Print build version.

Run 'undertow help COMMAND' for flags and examples, or see README.md.
All sessions are encrypted and signed by the server. Open enrollment allows
any reachable client; trust-on-first-use cannot verify the first contact.
`
	case "init":
		body = `undertow init — create server credentials

Usage: undertow init [--identity PATH] [--token-file PATH]

  --identity PATH     Ed25519 server identity (default identity.key).
  --token-file PATH   Random enrollment token (default token.key).

Run once on the server. Record the printed fingerprint. Copy token.key
securely to agents and VPN clients; never copy identity.key to them.
The identity file is reused on subsequent starts. An existing token is kept.
`
	case "server":
		body = `undertow server — direct-DNS listener and optional proxy interface

Usage: undertow server [FLAGS]

Connection and identity:
  --listen IP:PORT          UDP listener (default 0.0.0.0:53).
  --domain NAME             Synthetic DNS name (default t.undertow.invalid).
  --identity PATH           Server Ed25519 key (default identity.key).
  --auth MODE               token (default), password, or none/open enrollment.
  --token-file PATH         Enrollment token for token mode (default token.key).
  --password TEXT           Password mode credential; visible in process list.
  --password-file PATH      Read password from file instead.

Use token or password enrollment for real deployments. With --auth none,
anyone who reaches the listener can join, access network paths, and run
commands on agents unless those agents use --deny-exec.

Internal pivot:
  --tun                     Create server proxy TUN/Wintun for routed pivots.
  --tun-name NAME           Proxy adapter name (default undertow0).
  --tunnel-address CIDR     Proxy adapter address (default 172.16.254.1/24).
  --forward LOCAL=REMOTE    Repeatable local TCP forward through an agent.
  --via-agent ID            Agent for --forward when several are connected.

Operator API and diagnostics:
  --control-listen IP:PORT  Loopback API (default 127.0.0.1:47889).
  --control-token-file PATH Local API token (default control.key).
  --probe-echo              Echo transport probes; disables normal streams.

Lifecycle:
  --foreground              Run attached to the terminal (default).
  --background              Start a detached process, log and PID file.
  --stop                    Gracefully stop a background server.
  --log-file PATH           Background log (default undertow-server.log).
  --pid-file PATH           Background state (default undertow-server.pid).

Examples:
  undertow server --listen 0.0.0.0:53 --foreground
  sudo undertow server --listen 0.0.0.0:53 --tun --background
  sudo undertow server --stop

The server proxy TUN is for internal pivot routes; VPN Internet egress uses
server sockets and does not require --tun. Use one 'server' subcommand only.
`
	case "agent":
		body = `undertow agent — unprivileged connector on an internal host

Usage: undertow agent --server IP:PORT [--fingerprint HEX | --trust-on-first-use] [FLAGS]

  --server IP:PORT         Direct-DNS server IPv4 and UDP port (required).
  --fingerprint HEX        Pinned server public-key fingerprint.
  --fingerprint-file PATH  Saved pin (default server.fingerprint).
  --trust-on-first-use     Discover and save pin after first authenticated
                           connection; opt in only if first contact is trusted.
  --auth MODE             token (default), password, or none/open enrollment.
  --token-file PATH        Enrollment token for token mode (default token.key).
  --password TEXT         Password mode credential; visible in process list.
  --password-file PATH    Read password from file instead.
  --agent-key PATH         Agent Ed25519 identity (default agent.key).
  --deny-exec              Disable operator executable commands.
  --advertise-route CIDR    Offer an additional IPv4 route to VPN clients;
                           repeatable. Up IPv4 interfaces are also offered.
  --domain NAME            Match server --domain (default t.undertow.invalid).
  --payload-profile MODE   auto, large, or small (default auto).
  --probe                  Run encrypted echo probes instead of sockets.
  --probe-count N          Stop after N probes; 0 keeps running.
  --probe-size BYTES       Probe payload (16–65536; default 64).
  --probe-interval D       Delay between probes (default 1s; 0=max speed).

  --foreground             Run attached to the terminal (default).
  --background             Run detached with a log and PID file.
  --stop                   Gracefully stop a background agent.
  --log-file PATH          Default undertow-agent.log.
  --pid-file PATH          Default undertow-agent.pid.

Example: undertow agent --server 203.0.113.10:53 --fingerprint HEX --token-file token.key --background

The agent changes no interface or host route. After it connects, run
'undertow status' on the server and add an internal route through its ID.
Operator command execution is enabled by default; use --deny-exec to turn
it off on this agent. Connected VPN clients can request commands.
Operator subcommands: 'undertow agent list|show ID|select ID'.
`
	case "client":
		body = `undertow client — privileged IPv4 VPN on a separate host

Usage: undertow client --vpn --server IP:PORT [--fingerprint HEX | --trust-on-first-use] [FLAGS]

  --vpn                    Required; install IPv4 VPN routes.
  --internal               Also send configured agent pivot routes through
                           their agents; requires server route setup.
  --server IP:PORT         Direct-DNS server IPv4 and UDP port (required).
  --fingerprint HEX        Pinned server public-key fingerprint.
  --fingerprint-file PATH  Saved pin (default server.fingerprint).
  --trust-on-first-use     Discover and save pin after first authenticated
                           connection; opt in only if first contact is trusted.
  --auth MODE             token (default), password, or none/open enrollment.
  --token-file PATH        Enrollment token for token mode (default token.key).
  --password TEXT         Password mode credential; visible in process list.
  --password-file PATH    Read password from file instead.
  --client-key PATH        Client Ed25519 identity (default client.key).
  --domain NAME            Match server --domain (default t.undertow.invalid).
  --tun-name NAME          Local adapter (default undertow-vpn).
  --tunnel-address CIDR    Local address (default 172.16.253.1/24).
  --payload-profile MODE   auto, large, or small (default auto).
  --verify-url URL         Public IPv4 check (default https://api.ipify.org);
                           empty value skips verification.
  --interactive            Open a console even when input is redirected;
                           terminal starts open it by default.
  --routes-file PATH       Persist accepted client routes (default client-routes.json).

  --foreground             Open an attached console when in a terminal.
  --background             Run detached with a log and PID file.
  --stop                   Gracefully stop and remove owned VPN routes.
  --log-file PATH          Default undertow-client.log.
  --pid-file PATH          Default undertow-client.pid.

Example: sudo undertow client --vpn --server 203.0.113.10:53 --fingerprint HEX --token-file token.key --background

Without --internal, Internet traffic exits through server sockets. With
--internal, only server configured pivot subnets use agents. The local .1
address belongs to the client adapter; it is not the public egress address.
In a terminal, client --vpn opens the interactive console by default. The
VPN worker stays running when you type 'background'; use 'undertow client
attach' to return. 'quit' stops the worker and removes its routes. Ctrl+C
asks for confirmation before stopping. Up/Down recall commands; Tab
completes top-level commands. A scripted client without a terminal runs in
the foreground and accepts a later 'client attach' from another terminal.
Use 'client attach --pid-file PATH' for a custom state file.
VPN clients can view status, accept
advertised routes or add manual local routes through agents, change their
own internal mode, and execute on agents that allow it. Accepted routes
are restored on reconnect. Server global route and selection controls
remain on the server host. No extra token is needed.
Type 'agents', then 'use 1' to enter an agent. Run 'exec id' or 'route add
CIDR' there; 'back' returns to the main menu. Important connection and agent
events appear in the console; routine logs go to --log-file.
`
	case "console":
		body = `undertow console — interactive operator console on the server host

Usage: undertow console [--control IP:PORT] [--control-token-file PATH]

Connects to the running server's loopback API. Use status, routes, route add,
route del, select, exec, help, and quit inside the console. Agent execution
is enabled on agents by default and runs a named program with arguments,
without an implicit shell. Use 'undertow console' on the server host;
'undertow client --vpn --interactive ...' opens a VPN client console.
In either console, type 'agents' to list numbered agents, 'use 1' to enter
one, 'help' for the current menu, and 'back' to return to the main menu.
`
	case "status":
		body = `undertow status — inspect agents, VPN clients, and routes

Usage: undertow status [--json] [--control IP:PORT] [--control-token-file PATH]

  --json                    Machine-readable output.
  --control IP:PORT         Loopback API (default 127.0.0.1:47889).
  --control-token-file PATH Local API token (default control.key).

Run on the server host while the server is active. This token is separate
from the enrollment token shared with agents and clients.
`
	case "route":
		body = `undertow route — map internal subnets to connected agents

Usage:
  undertow route add CIDR --via AGENT_ID
  undertow route list [--json]
  undertow route del CIDR

  --via AGENT_ID            Agent shown by 'undertow status'.
  --json                    Machine-readable list output.
  --control IP:PORT         Loopback API (default 127.0.0.1:47889).
  --control-token-file PATH Local API token (default control.key).

Start the server with --tun for server-host routed pivots. A VPN client
uses these routes only when started with --internal. Routes are inactive
while their agent is disconnected.
`
	case "session":
		body = `undertow session — manage a live agent session

Usage: undertow session kill AGENT_ID [--control IP:PORT] [--control-token-file PATH]

AGENT_ID is shown by 'undertow status'. This disconnects the current
session; a running agent may reconnect automatically.
`
	case "version":
		body = "undertow version — print the build version and commit.\n"
	default:
		return fmt.Errorf("unknown help topic %q", topic)
	}
	_, err := io.WriteString(w, body)
	return err
}
