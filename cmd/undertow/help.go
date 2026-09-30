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
		body = `Undertow — encrypted access to internal networks and IPv4 Internet egress

What do you want to do? Run 'undertow examples' for commands by machine.
Use 'undertow help server|agent|client' for all flags.

Roles:
  server   Accepts connections and opens its operator console in a terminal.
           Add --tun when the server host needs internal routes.
  agent    Runs without elevation where internal targets are reachable.
  client   Routes traffic from this host through a local TUN/Wintun; elevated.

An agent exposes destinations reachable from its host, without changing that
host's routes. A client routes selected prefixes on its own host; --internal
alone leaves its Internet/default route unchanged. A client does not expose
its local network; an agent does not provide VPN Internet egress to its own
host. For access to agent networks, connect with --internal, then accept an
advertised route or add a manual route in the client console.

Setup:
  1. On the server: undertow init
  2. Choose enrollment: token (default), password, or open (--auth none).
     Pin the server fingerprint or opt in to --trust-on-first-use.
  3. Start: undertow server --listen 0.0.0.0:53
  4. Start an agent or client using --server and matching --auth options.

Commands:
  init       Create server identity and enrollment token.
  server     Run a carrier listener and operator console; 'server attach' returns.
  agent      Connect an internal host without changing its routes.
  client     Run a privileged IPv4 tunnel; 'client attach' opens its console.
  console    Open an interactive server operator console.
  status     Show connected agents, VPN clients, and routes.
  route      Add, remove, or list agent pivot routes.
  session    Disconnect an agent session.
  examples   Show common setups with commands by machine.
  doctor     Check local prerequisites before starting a role.
  version    Print build version.

Transport: DNS (default, UDP/53) for restrictive paths; WebSocket (TCP/443)
for HTTPS egress; QUIC (UDP/443) when that path is available. Select the same
--transport on server, agent, and client. See docs/quickstart.md.
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
		body = `undertow server — carrier listener and operator console

Usage: undertow server [FLAGS]
       undertow server attach [--pid-file PATH] [--control IP:PORT]

Connection and identity:
  --transport MODE          dns (default), websocket, or quic.
  --listen IP:PORT          Listener (DNS UDP/53; WebSocket TCP/443; QUIC UDP/443).
  --domain NAME             Synthetic DNS name (DNS only; default t.undertow.invalid).
  --websocket-path PATH     WebSocket URL path (default /undertow).
  --tls-cert PATH           TLS certificate PEM (WebSocket and QUIC).
  --tls-key PATH            TLS private key PEM (WebSocket and QUIC).
  --tls-self-signed         Generate a temporary TLS certificate in memory.
  --identity PATH           Server Ed25519 key (default identity.key).
  --auth MODE               token (default), password, or none/open enrollment.
  --token-file PATH         Enrollment token for token mode (default token.key).
  --token HEX               Token value instead of a file; visible in process list.
  --password TEXT           Password mode credential; visible in process list.
  --password-file PATH      Read password from file instead.

Use token or password enrollment for real deployments. With --auth none,
anyone who reaches the listener can join, access network paths, and run
commands on agents unless those agents restrict capabilities with --deny.

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
  --foreground              Run a foreground worker without the console.
  --background              Start a detached worker, log and PID file.
  --stop                    Gracefully stop a background server.
  --log-file PATH           Background log (default undertow-server.log).
  --pid-file PATH           Background state (default undertow-server.pid).

Examples:
  sudo undertow server --listen 0.0.0.0:53
  sudo undertow server attach
  sudo undertow server --listen 0.0.0.0:53 --tun --background
  sudo undertow server --transport websocket --tls-cert server.crt --tls-key server.key
  sudo undertow server --transport quic --tls-cert server.crt --tls-key server.key
  sudo undertow server --transport websocket --tls-self-signed
  sudo undertow server --transport quic --tls-self-signed
  sudo undertow server --stop

In a terminal, server starts a worker and opens the operator console.
Type 'agents', 'use 1', and 'routes'. 'background', 'quit', and 'exit'
detach while the server continues. 'logs' shows recent worker output;
'logs follow' streams it until Enter. Type 'stop' to shut down gracefully.
Use 'server attach' to return after detaching. Custom --pid-file,
--control, --control-token-file, and --log-file values must be passed again
to attach. Use --foreground for a service manager or direct debugging.

The server proxy TUN is for internal pivot routes; VPN Internet egress uses
server sockets and does not require --tun. Use one 'server' subcommand only.
`
	case "agent":
		body = `undertow agent — unprivileged connector on an internal host

Usage: undertow agent --server HOST:PORT [--fingerprint HEX | --trust-on-first-use] [FLAGS]

  --server HOST:PORT       Server host and carrier port; DNS needs numeric IPv4.
  --transport MODE         dns (default), websocket, or quic; match server.
  --websocket-path PATH    Match server path for WebSocket (default /undertow).
  --tls-server-name NAME   Verify a DNS name in the TLS certificate.
  --tls-insecure-skip-verify  Allow a private/self-signed TLS certificate;
                           pin the Undertow fingerprint separately.
  --fingerprint HEX        Pinned server public-key fingerprint.
  --fingerprint-file PATH  Saved pin (default server.fingerprint).
  --trust-on-first-use     Discover and save pin after first authenticated
                           connection; opt in only if first contact is trusted.
  --auth MODE             token (default), password, or none/open enrollment.
  --token-file PATH        Enrollment token for token mode (default token.key).
  --token HEX              Token value instead of a file; visible in process list.
  --password TEXT         Password mode credential; visible in process list.
  --password-file PATH    Read password from file instead.
  --agent-key PATH         Agent Ed25519 identity (default agent.key).
  --deny LIST              Disable agent capabilities independently. Names:
                           pivot,exec,hostops,interactive,scripts,wasm,upload,download,listeners.
  --advertise-route CIDR    Offer an additional IPv4 route to VPN clients;
                           repeatable. Up IPv4 interfaces are also offered.
  --domain NAME            Match server --domain (DNS only).
  --payload-profile MODE   DNS only: auto, large, or small (default auto).
  --probe                  Run encrypted echo probes instead of sockets.
  --probe-count N          Stop after N probes; 0 keeps running.
  --probe-size BYTES       Probe payload (16–65536; default 64).
  --probe-interval D       Delay between probes (default 1s; 0=max speed).

  --foreground             Run attached to the terminal (agent default).
  --background             Run detached with a log and PID file.
  --stop                   Gracefully stop a background agent.
  --log-file PATH          Default undertow-agent.log.
  --pid-file PATH          Default undertow-agent.pid.

Example: undertow agent --server 203.0.113.10:53 --fingerprint HEX --token-file token.key --background

The agent changes no interface or host route. After it connects, run
'undertow status' on the server and add an internal route through its ID.
All implemented capabilities are enabled by default. For example, use
--deny=exec,upload to reject those operations while allowing built-in host
operations and downloads. Add hostops to deny the built-in commands.
Connected VPN clients can request allowed operations. 'status' shows the
agent's supported and allowed capabilities.
Operator subcommands: 'undertow agent list|show ID|select ID'.
`
	case "client":
		body = `undertow client — privileged IPv4 tunnel on a separate host

Usage: undertow client (--vpn | --internal | --vpn --internal) --server HOST:PORT [FLAGS]

  --vpn                    Install two IPv4 /1 routes for Internet egress.
  --internal               Use agent routes accepted or added in the client
                           console; server global routes are optional. Alone,
                           this leaves Internet/default routes unchanged.
  --server HOST:PORT       Server host and carrier port; DNS needs numeric IPv4.
  --transport MODE         dns (default), websocket, or quic; match server.
  --websocket-path PATH    Match server path for WebSocket (default /undertow).
  --tls-server-name NAME   Verify a DNS name in the TLS certificate.
  --tls-insecure-skip-verify  Allow a private/self-signed TLS certificate;
                           pin the Undertow fingerprint separately.
  --fingerprint HEX        Pinned server public-key fingerprint.
  --fingerprint-file PATH  Saved pin (default server.fingerprint).
  --trust-on-first-use     Discover and save pin after first authenticated
                           connection; opt in only if first contact is trusted.
  --auth MODE             token (default), password, or none/open enrollment.
  --token-file PATH        Enrollment token for token mode (default token.key).
  --token HEX              Token value instead of a file; visible in process list.
  --password TEXT         Password mode credential; visible in process list.
  --password-file PATH    Read password from file instead.
  --client-key PATH        Client Ed25519 identity (default client.key).
  --domain NAME            Match server --domain (DNS only).
  --tun-name NAME          Local adapter (default undertow-vpn).
  --tunnel-address CIDR    Local address (default 172.16.253.1/24).
  --payload-profile MODE   DNS only: auto, large, or small (default auto).
  --verify-url URL         Public IPv4 check for --vpn only (default
                           https://api.ipify.org); empty skips verification.
  --interactive            Open a console even when input is redirected;
                           terminal starts open it by default.
  --routes-file PATH       Persist accepted client routes (default client-routes.json).

  --foreground             Run a foreground worker without a console.
  --background             Run detached with a log and PID file.
  --stop                   Gracefully stop and remove owned VPN routes.
  --log-file PATH          Default undertow-client.log.
  --pid-file PATH          Default undertow-client.pid.

Examples (on the client host):
  sudo undertow client --vpn --server 203.0.113.10:53 --fingerprint HEX --token-file token.key
  sudo undertow client --internal --server 203.0.113.10:53 --fingerprint HEX --token-file token.key
  sudo undertow client --vpn --internal --server 203.0.113.10:53 --fingerprint HEX --token-file token.key

--internal alone pins the carrier server route without changing Internet/default
routes or checking public egress. After connecting, type 'agents', 'use 1',
'routes', then 'route accept CIDR' or 'route add CIDR'. No server route command
is needed for per-client routes; active global server routes also work.
--vpn adds two /1 routes and checks public egress by default. The local .1
address belongs to the client adapter; it is not the public egress address.
In a terminal, client opens the interactive console by default. The
client worker stays running when you type 'background'; use 'undertow client
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
		body = `undertow console — attach to a running server's operator API

Usage: undertow console [--control IP:PORT] [--control-token-file PATH]

Connects to the running server's loopback API. Use status, routes, route add,
route del, select, exec, help, and quit inside the console. Agent execution
is enabled on agents by default and runs a named program with arguments,
without an implicit shell. Use 'undertow console' on the server host;
'undertow client --internal --interactive ...' opens a client console.
In either console, type 'agents' to list numbered agents, 'use 1' to enter
one, 'help' for the current menu, and 'back' to return to the main menu.
Inside the selected agent, 'shell' opens a live session; Ctrl-] returns to
Undertow without stopping the VPN. --deny=interactive blocks these sessions.
Use 'run-script bash LOCAL_FILE' or 'run-script powershell LOCAL_FILE' to send
source to the agent's interpreter without storing a script there. Add
'--background' before the language to keep it in the job list. --deny=scripts
blocks these runs independently of exec and interactive sessions.
Use 'run-wasm MODULE_FILE [ARGS]' to execute a WASI module from memory. Add
'--background' to make a job; '--stdin LOCAL_FILE' supplies up to 64 KiB of
stdin. --deny=wasm blocks it independently. Modules are limited to 4 MiB,
16 MiB guest memory, 4 MiB output, 2 minutes and two concurrent agent runs.
For normal terminal use, 'undertow server' opens this console automatically;
'undertow server attach' reconnects to its worker after detaching.
Use 'job start PROGRAM [ARGS]' inside the agent menu for a task that should
continue while the console is detached. 'jobs', 'job show ID', 'job output ID',
and 'job cancel ID' manage it.
Use 'show' in the selected agent menu for detailed telemetry and routes.
Inside an agent, use built-in pwd, ls, stat, mkdir, rm, whoami, ps,
privileges, env, interfaces, dns, and route-table commands. --deny=exec
blocks arbitrary programs while leaving these built-ins available;
--deny=hostops blocks the built-ins separately.
The VPN client console also supports per-agent TCP 'forward add/list/del'.
For example, after 'use 1': forward add 0.0.0.0:8080 127.0.0.1:8080.
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
	case "examples":
		return writeExamples(w)
	case "doctor":
		body = `undertow doctor — read-only local startup checks

Usage: undertow doctor server|agent|client [FLAGS]

Common:
  --auth MODE              token (default), password, or none.
  --token-file PATH        Enrollment token (default token.key).
  --password-file PATH     Password file when --auth password.
  --pid-file PATH          Background state file for this role.

Server:
  --transport MODE         dns (default), websocket, or quic.
  --listen IP:PORT         Carrier listener (DNS :53, WebSocket/QUIC :443).
  --tls-cert PATH          TLS certificate for WebSocket or QUIC server.
  --tls-key PATH           TLS private key for WebSocket or QUIC server.
  --tls-self-signed        Generate a temporary TLS certificate in memory.
  --control-listen IP:PORT Local operator API (default 127.0.0.1:47889).
  --identity PATH          Server key (default identity.key).
  --tun                    Check TUN/Wintun and tunnel network.
  --forward LOCAL=REMOTE   Check a planned local TCP forward; repeatable.

Agent and client:
  --server HOST:PORT       Server host and carrier port; DNS needs numeric IPv4.
  --tls-server-name NAME   TLS certificate name for WebSocket or QUIC.
  --tls-insecure-skip-verify  Permit private/self-signed TLS certificate.
  --fingerprint HEX        Explicit server fingerprint; otherwise check file.
  --fingerprint-file PATH  Saved pin (default server.fingerprint).
  --trust-on-first-use     Allow first contact without a saved pin.

Client:
  --tunnel-address CIDR   Client TUN network (default 172.16.253.1/24).
  --route CIDR            Check an intended internal route; repeatable.

Use --tunnel-address 172.16.254.1/24 for a server proxy TUN by default.
Doctor makes no network or route changes. FAIL exits nonzero; WARN calls out
checks that need operator attention.
`
	case "version":
		body = "undertow version — print the build version and commit.\n"
	default:
		return fmt.Errorf("unknown help topic %q", topic)
	}
	_, err := io.WriteString(w, body)
	return err
}

func writeExamples(w io.Writer) error {
	_, err := io.WriteString(w, `Undertow examples (direct DNS on UDP/53)

First, on SERVER: undertow init
Copy token.key securely to AGENT/CLIENT; keep identity.key on SERVER.
Replace SERVER_IP, FINGERPRINT, and example addresses.
SERVER usually needs sudo for UDP/53; --tun and CLIENT need root/Admin.

pivot — reach an internal subnet from SERVER through AGENT:
  SERVER  sudo undertow server --tun --listen 0.0.0.0:53
  AGENT  undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
  SERVER console  agents
  SERVER console  use 1
  SERVER console  show
  SERVER console  route add 10.20.0.0/16

internal — reach an agent network from CLIENT, keeping normal Internet:
  SERVER  sudo undertow server --listen 0.0.0.0:53
  AGENT  undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
  CLIENT  sudo undertow client --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
  CLIENT console  agents
  CLIENT console  use 1
  CLIENT console  routes
  CLIENT console  route accept 10.20.0.0/16

vpn — route CLIENT IPv4 Internet traffic through SERVER:
  SERVER  sudo undertow server --listen 0.0.0.0:53
  CLIENT  sudo undertow client --vpn --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key

vpn-internal — Internet through SERVER, internal subnet through AGENT:
  SERVER  sudo undertow server --listen 0.0.0.0:53
  AGENT  undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
  CLIENT  sudo undertow client --vpn --internal --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
  CLIENT console  agents
  CLIENT console  use 1
  CLIENT console  routes
  CLIENT console  route accept 10.20.0.0/16

forward — reach one internal TCP service without a TUN:
  SERVER  sudo undertow server --listen 0.0.0.0:53 --forward 127.0.0.1:18080=10.20.0.50:80
  AGENT  undertow agent --server SERVER_IP:53 --fingerprint FINGERPRINT --token-file token.key
  SERVER  curl http://127.0.0.1:18080/

Use 'route add CIDR' in the client console if a reachable route is not
advertised. Type 'quit' in the client console to stop it and remove routes.
On the server, 'quit' detaches and 'stop' shuts down. Ctrl+C stops a
foreground agent. Use the same console commands with --transport websocket
(TCP/443) or --transport quic (UDP/443) on all roles; the server needs
--tls-cert and --tls-key, or --tls-self-signed. See docs/quickstart.md for full steps.
`)
	return err
}
