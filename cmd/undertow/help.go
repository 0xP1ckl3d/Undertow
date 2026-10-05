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
  2. Bootstrap the first Team Leader: undertow operators bootstrap --id ID
     --display-name NAME --password-file PATH.
     Choose enrollment: token (default), password, or open (--auth none).
     Pin the server fingerprint or opt in to --trust-on-first-use.
  3. Start: undertow server (DNS UDP/53, WebSocket TCP/443, QUIC UDP/443)
  4. Start an agent or client using --server and matching --auth options.

Commands:
  init       Create server identity and enrollment token.
  operators Bootstrap the first server-managed Team Leader.
  server     Run shared carrier listeners and operator console; 'server attach' returns.
  agent      Connect an internal host without changing its routes.
  client     Run a privileged IPv4 tunnel; 'client attach' opens its console.
  console    Open an interactive server operator console.
  status     Show connected agents, VPN clients, and routes.
  route      Add, remove, or list agent pivot routes.
  session    Disconnect an agent session.
  examples   Show common setups with commands by machine.
  doctor     Check local prerequisites before starting a role.
  bof        Inspect a Windows AMD64 Beacon Object File.
  version    Print build version.

Transport: the server listens on QUIC UDP/443, WebSocket TCP/443 and DNS UDP/53
by default. Each agent and client chooses one listener independently. Use DNS
when direct UDP DNS is the available path. See docs/quickstart.md.
Run 'undertow help COMMAND' for flags and examples, or see README.md.
All sessions are encrypted and signed by the server. Client sessions also
require a server-managed operator account, even with open enrollment.
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
	case "operators":
		body = `undertow operators bootstrap — create the first Team Leader locally

Usage: undertow operators bootstrap [--operations-db PATH] [--id ID] [--display-name NAME] [--password-file PATH]

Run before the first server start. The account database must be empty.
Use the same --operations-db path as the server (default operations.db).
The ID and password can also come from UNDERTOW_OPERATOR_ID and
UNDERTOW_OPERATOR_PASSWORD, or hidden prompts in an interactive terminal.
Passwords are stored as hashes. Later account
management is available to connected Team Leaders in the client console and GUI.
See docs/operator-authentication.md.
`
	case "server":
		body = `undertow server — carrier listener and operator console

Usage: undertow server [FLAGS]
       undertow server attach [--pid-file PATH] [--control IP:PORT]

Connection and identity:
  --transport LIST          dns,websocket,quic (default); comma-separated subset.
  --listen IP:PORT          Generic address for one explicitly selected carrier.
  --dns-listen IP:PORT      DNS UDP address (default 0.0.0.0:53).
  --websocket-listen IP:PORT WebSocket TCP address (default 0.0.0.0:443).
  --quic-listen IP:PORT     QUIC UDP address (default 0.0.0.0:443).
  --domain NAME             Synthetic DNS name (DNS only; default t.undertow.invalid).
  --websocket-path PATH     WebSocket URL path (default /undertow).
  --deployment-profile PATH JSON carrier and callback settings; see docs/deployment-profiles.md.
  --tls-cert PATH           TLS certificate PEM (WebSocket and QUIC).
  --tls-key PATH            TLS private key PEM (WebSocket and QUIC).
  --tls-self-signed         Explicitly request temporary TLS (automatic without files).
  --identity PATH           Server Ed25519 key (default identity.key).
  --auth MODE               token (default), password, or none/open enrollment.
  --token-file PATH         Enrollment token for token mode (default token.key).
  --token HEX               Token value instead of a file; visible in process list.
  --password TEXT           Password mode credential; visible in process list.
  --password-file PATH      Read password from file instead.

Bootstrap an operator account before starting the server. With --auth none,
agent enrollment is open; clients still require operator credentials.

Internal pivot:
  --tun                     Create server proxy TUN/Wintun for routed pivots.
  --tun-name NAME           Proxy adapter name (default undertow0).
  --tunnel-address CIDR     Proxy adapter address (default 172.16.254.1/24).
  --forward LOCAL=REMOTE    Repeatable local TCP forward through an agent.
  --via-agent ID            Agent for --forward when several are connected.

Operator API and diagnostics:
  --control-listen IP:PORT  Loopback API (default 127.0.0.1:47889).
  --control-token-file PATH Local API token (default control.key).
  --payload-retrieval-path PATH Opaque payload download prefix (default /).
  --probe-echo              Echo transport probes; disables normal streams.

Lifecycle:
  --foreground              Run a foreground worker without the console.
  --background              Start a detached worker, log and PID file.
  --stop                    Gracefully stop a background server.
  --log-file PATH           Background log (default undertow-server.log).
  --pid-file PATH           Background state (default undertow-server.pid).

Examples:
  sudo undertow server
  sudo undertow server attach
  sudo undertow server --tun --background
  sudo undertow server --transport websocket --tls-cert server.crt --tls-key server.key
  sudo undertow server --transport quic --tls-cert server.crt --tls-key server.key
  sudo undertow server --transport websocket --tls-self-signed
  sudo undertow server --transport quic --tls-self-signed
  sudo undertow server --stop

In a terminal, server starts a worker and opens the operator console.
Type 'agents', 'use 1', and 'routes'. 'background', 'quit', and 'exit'
detach while the server continues. 'logs' shows recent worker output;
'logs follow' streams it until Enter. Type 'stop' to shut down gracefully.
Use 'transports' for listener state, 'start transport NAME' to add one,
and 'stop transport NAME [force]' to remove one. Active sessions require
the explicit force form. 'topology' shows direct and relayed agents.
Use 'server attach' to return after detaching. Custom --pid-file,
--control, --control-token-file, and --log-file values must be passed again
to attach. Use --foreground for a service manager or direct debugging.

The server proxy TUN is for internal pivot routes; VPN Internet egress uses
server sockets and does not require --tun. Use one 'server' subcommand only.
`
	case "agent":
		body = `undertow agent — unprivileged connector on an internal host

Usage: undertow agent --server HOST:PORT|PIPE_PATH [--fingerprint HEX | --trust-on-first-use] [FLAGS]

  --server ADDRESS         Host:port for network carriers, \\HOST\pipe\NAME for relay-smb.
  --transport MODE         dns (default), websocket, quic, relay, or relay-smb.
  --websocket-path PATH    Match server path for WebSocket (default /undertow).
  --deployment-profile PATH JSON carrier and callback settings; see docs/deployment-profiles.md.
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
                           pivot,exec,hostops,interactive,scripts,wasm,native,upload,download,listeners,relay.
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

For a child agent, select its parent in a server or connected client console, run
'relay start INTERNAL_IP:8443', then start the child with
'--transport relay --server INTERNAL_IP:8443 --fingerprint HEX'.
Use a separate --agent-key. No relay listener opens by default;
--deny=relay rejects the operator request independently of listeners.
For a Windows SMB named-pipe relay, start \\.\pipe\NAME on the parent and
use '--transport relay-smb --server \\PARENT_HOST\pipe\NAME' on a Windows child.

The agent changes no interface or host route. After it connects, run
'undertow status' on the server and add an internal route through its ID.
All implemented capabilities are enabled by default. For example, use
--deny=exec,upload to reject those operations while allowing built-in host
operations and downloads. Add hostops to deny the built-in commands.
Connected VPN clients can request allowed operations. 'status' shows the
agent's supported and allowed capabilities.
Operator subcommands: 'undertow agent list|show ID|select ID'.
In a server or client console, type 'payload' for the guided deployment
workflow. Use 'payload profile create NAME', 'payload build NAME PLATFORM
ARCH', and 'payload host PAYLOAD_ID' to produce and distribute a
zero-argument thin-agent executable from a prebuilt template. A payload
ID identifies a build; a connected agent has a separate agent ID.
`
	case "client":
		body = `undertow client — privileged IPv4 tunnel on a separate host

Usage: undertow client (--vpn | --internal | --vpn --internal) --server HOST:PORT [FLAGS]

  --vpn                    Install two IPv4 /1 routes for Internet egress.
  --internal               Use agent routes accepted or added in the client
                           console; server global routes are optional. Alone,
                           this leaves Internet/default routes unchanged.
  --server ADDRESS         Host:port for network carriers; relay-smb uses a
                           Windows named-pipe path.
  --transport MODE         dns (default), websocket, quic, relay, or relay-smb.
  --websocket-path PATH    Match server path for WebSocket (default /undertow).
  --deployment-profile PATH JSON carrier and callback settings; see docs/deployment-profiles.md.
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
  --operator ID           Server-managed account (or UNDERTOW_OPERATOR_ID).
  --operator-password-file PATH  Password file (or UNDERTOW_OPERATOR_PASSWORD).
                           Missing values are prompted in an interactive terminal.
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
  sudo undertow client --vpn --server 203.0.113.10:53 --fingerprint HEX --token-file token.key --operator alice --operator-password-file alice.password
  sudo undertow client --internal --server 203.0.113.10:53 --fingerprint HEX --token-file token.key --operator alice --operator-password-file alice.password
  sudo undertow client --vpn --internal --server 203.0.113.10:53 --fingerprint HEX --token-file token.key --operator alice --operator-password-file alice.password

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

Connects to the running server's loopback API. Normal terminal use starts
this console automatically with 'undertow server'; 'server attach' returns
after detaching. Type 'agents', 'use 1', and 'show' to inspect an agent.
'help' shows the full menu for the current level. Type 'help relay', 'help route', 'help
run-script', or another topic for detailed usage. Tab completes command
names and local script, WASM, native module, BOF, and transfer paths. 'clear' or 'cls' clears the
screen. The client console supports 'vpn on|off|status' and
'internal on|off|status'. Ctrl-] exits a live shell
without closing the Undertow console. See docs/console.md for the full guide.
`
	case "relay", "topology", "transport", "transports", "run-script", "run-wasm", "run-native", "run-bof", "shell", "jobs", "host":
		return printConsoleHelp(w, false, true, false, topic)
	case "bof":
		body = `undertow bof inspect FILE.o — inspect BOF compatibility locally

Parses an AMD64 COFF object and reports sections, symbols, relocations,
Windows imports, Beacon imports, and compatibility errors. Execution uses
the same parser. See docs/bof-compatibility.md.
`
	case "forward", "upload", "download", "internal":
		return printConsoleHelp(w, true, true, false, topic)
	case "status":
		body = `undertow status — inspect all active server listeners, agents, VPN clients, and routes

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
  --transport LIST         dns,websocket,quic (default); comma-separated subset.
  --listen IP:PORT         Address for one selected carrier only.
  --dns-listen IP:PORT     DNS UDP address (default 0.0.0.0:53).
  --websocket-listen IP:PORT WebSocket TCP address (default 0.0.0.0:443).
  --quic-listen IP:PORT    QUIC UDP address (default 0.0.0.0:443).
  --tls-cert PATH          TLS certificate for WebSocket or QUIC server.
  --tls-key PATH           TLS private key for WebSocket or QUIC server.
  --tls-self-signed        Explicit temporary TLS (automatic without files).
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
	_, err := io.WriteString(w, `Undertow examples — QUIC on UDP/443

On SERVER, run 'undertow init' once. Copy token.key to AGENT and CLIENT;
keep identity.key on SERVER. Replace SERVER_IP and FINGERPRINT. The server
starts QUIC, WebSocket and DNS together. These IP-address examples use its
automatic self-signed TLS certificate and a pinned Undertow fingerprint.

vpn — Internet egress from CLIENT (no AGENT and no SERVER --tun):
  SERVER  sudo undertow server
  CLIENT  sudo undertow client --vpn --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify --operator alice --operator-password-file alice.password
  CLIENT  curl -4 https://api.ipify.org

internal — an AGENT network from CLIENT, keeping ordinary Internet unchanged:
  SERVER  sudo undertow server
  AGENT   undertow agent --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify --advertise-route 10.20.0.0/16
  CLIENT  sudo undertow client --internal --transport quic --server SERVER_IP:443 --fingerprint FINGERPRINT --tls-insecure-skip-verify --operator alice --operator-password-file alice.password
  CLIENT console (one command per line): agents, use 1, routes,
                                      route accept 10.20.0.0/16
  CLIENT  curl http://10.20.0.50/

vpn-internal — use --vpn --internal on CLIENT, then accept the AGENT route above.
When the AGENT has not advertised a reachable prefix, use 'route add CIDR'.

pivot — SERVER host itself needs an AGENT route: start 'sudo undertow server --tun',
connect the AGENT, then in SERVER console enter agents, use 1, route add CIDR.
SERVER --tun is unnecessary for any CLIENT mode or for Internet egress.

forward — one TCP service on an AGENT, without any TUN:
  SERVER  sudo undertow server --forward 127.0.0.1:18080=10.20.0.50:80
  AGENT   connect as above; then on SERVER: curl http://127.0.0.1:18080/

WebSocket: replace peer '--transport quic' with '--transport websocket';
keep port 443 and the TLS flag. DNS VPN: use '--transport dns --server
SERVER_IP:53' on the peer and omit the TLS flag. Each peer chooses its own
active server listener.

'background' detaches a console; 'server attach' or 'client attach' returns.
'quit' stops CLIENT and removes its routes; 'stop' shuts down SERVER.
See docs/quickstart.md for commands by host and verification.
`)
	return err
}
