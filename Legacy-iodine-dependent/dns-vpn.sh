#!/usr/bin/env bash
set -Eeuo pipefail

# ============================================================
# dns-vpn
#
# Simple iodine DNS tunnel server/client wrapper.
#
# Server:
#   sudo dns-vpn server -p "Password123"
#   sudo dns-vpn server -p "Password123" --background
#   sudo dns-vpn server --status
#   sudo dns-vpn server --stop
#
# Client:
#   sudo dns-vpn client -p "Password123" -s 203.0.113.10
#   sudo dns-vpn client -p "Password123" -s 203.0.113.10 --background
#   sudo dns-vpn client --status
#   sudo dns-vpn client --stop
# ============================================================

VERSION="1.0"

# Internal DNS tunnel addressing.
# This has NOTHING to do with the AWS private IP or target network.
TUN_IF="dns0"
TUN_NET="10.253.53.0/24"
TUN_SERVER_IP="10.253.53.1"
TUN_SERVER_CIDR="10.253.53.1/24"

# Arbitrary iodine top-domain when directly addressing the server.
TOPDOMAIN="tunnel.test"

RUNTIME_DIR="/run/dns-vpn"
LOG_DIR="/var/log/dns-vpn"

SERVER_STATE="${RUNTIME_DIR}/server.state"
CLIENT_STATE="${RUNTIME_DIR}/client.state"

SERVER_LOG="${LOG_DIR}/server.log"
CLIENT_LOG="${LOG_DIR}/client.log"

RULE_COMMENT="dns-vpn-server"

SCRIPT_SELF="$(
    cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1
    printf '%s/%s\n' "$PWD" "$(basename -- "${BASH_SOURCE[0]}")"
)"

# ============================================================
# COLOURS
# ============================================================

RESET="\033[0m"
BOLD="\033[1m"
DIM="\033[2m"

RED="\033[91m"
GREEN="\033[92m"
YELLOW="\033[93m"
CYAN="\033[96m"
WHITE="\033[97m"

ok() {
    echo -e "${GREEN}[+]${RESET} $*"
}

info() {
    echo -e "${CYAN}[*]${RESET} $*"
}

warn() {
    echo -e "${YELLOW}[!]${RESET} $*"
}

fail() {
    echo -e "${RED}[-]${RESET} $*"
}

die() {
    fail "$*"
    exit 1
}

heading() {
    echo
    echo -e "${CYAN}${BOLD}============================================================${RESET}"
    echo -e "${CYAN}${BOLD} $*${RESET}"
    echo -e "${CYAN}${BOLD}============================================================${RESET}"
    echo
}

# ============================================================
# HELP
# ============================================================

top_help() {
    cat <<EOF
dns-vpn ${VERSION}

Simple iodine DNS tunnel server/client wrapper.

Usage:

  dns-vpn server [options]
  dns-vpn client [options]

Examples:

  sudo dns-vpn server -p "Password123"
  sudo dns-vpn server -p "Password123" --background
  sudo dns-vpn server --status
  sudo dns-vpn server --stop

  sudo dns-vpn client -p "Password123" -s 203.0.113.10
  sudo dns-vpn client -p "Password123" -s 203.0.113.10 --background
  sudo dns-vpn client --status
  sudo dns-vpn client --stop

Help:

  dns-vpn -h
  dns-vpn server -h
  dns-vpn client -h
EOF
}


server_help() {
    cat <<EOF
dns-vpn server

Start and manage an iodine DNS tunnel server.

Usage:

  sudo dns-vpn server -p PASSWORD [--foreground|--background]
  sudo dns-vpn server --status
  sudo dns-vpn server --stop

Options:

  -p, --password PASSWORD
        Iodine tunnel password.

  -f, --foreground
        Run in the foreground.
        This is the default.

  -b, --background
        Start the server, display connection details, then background it.

  --status
        Show server status.

  --stop
        Stop a background or foreground-managed server.

  -h, --help
        Show this help.

The server automatically:

  - determines the Internet-facing interface
  - enables IPv4 forwarding
  - configures NAT for 10.253.53.0/24
  - determines the EC2/public IPv4 address
  - starts iodined on UDP/53
  - displays the exact client connection command
EOF
}


client_help() {
    cat <<EOF
dns-vpn client

Connect to a dns-vpn iodine server and route Internet traffic through it.

Usage:

  sudo dns-vpn client -p PASSWORD -s SERVER_IP [--foreground|--background]
  sudo dns-vpn client --status
  sudo dns-vpn client --stop

Options:

  -p, --password PASSWORD
        Iodine tunnel password.

  -s, --server SERVER_IP
        Public IPv4 address of the dns-vpn server.

  -f, --foreground
        Stay attached to the tunnel.
        Ctrl+C disconnects and restores normal routing.
        This is the default.

  -b, --background
        Establish the tunnel, display connection details, then background it.

  --status
        Show client/tunnel status.

  --stop
        Disconnect the tunnel and restore normal routing.

  -h, --help
        Show this help.

The client automatically:

  - determines the current route to the server
  - pins the DNS transport to the existing physical network
  - preserves existing DNS resolver routes
  - establishes the iodine tunnel
  - installs a preferred default route through dns0
  - restores normal routing when disconnected
EOF
}

# ============================================================
# BASIC HELPERS
# ============================================================

require_root() {
    [[ "${EUID}" -eq 0 ]] || die "Run this command with sudo/root."
}


ensure_dirs() {
    mkdir -p "$RUNTIME_DIR" "$LOG_DIR"
    chmod 700 "$RUNTIME_DIR"
    chmod 750 "$LOG_DIR"
}


valid_ipv4() {
    [[ "$1" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]
}


print_dependency_help() {
    local role="$1"
    shift
    local missing=("$@")

    echo
    fail "Missing required dependencies: ${missing[*]}"
    echo

    echo -e "${BOLD}Kali / Debian / Ubuntu:${RESET}"

    if [[ "$role" == "server" ]]; then
        echo
        echo '  sudo apt update && sudo apt install -y iodine iptables iproute2 curl procps'
    else
        echo
        echo '  sudo apt update && sudo apt install -y iodine iproute2 curl'
    fi

    echo
}


check_dependencies() {
    local role="$1"
    local required=()
    local missing=()

    if [[ "$role" == "server" ]]; then
        required=(
            iodined
            ip
            iptables
            sysctl
            curl
            awk
            grep
            nohup
        )
    else
        required=(
            iodine
            ip
            curl
            awk
            grep
            nohup
        )
    fi

    for cmd in "${required[@]}"; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            missing+=("$cmd")
        fi
    done

    if [[ ${#missing[@]} -gt 0 ]]; then
        print_dependency_help "$role" "${missing[@]}"
        exit 1
    fi
}


process_alive() {
    local pid="${1:-}"

    [[ -n "$pid" ]] &&
    [[ "$pid" =~ ^[0-9]+$ ]] &&
    kill -0 "$pid" 2>/dev/null
}


detect_public_ip() {
    local token=""
    local public_ip=""

    # --------------------------------------------------------
    # AWS EC2 IMDSv2 first.
    # --------------------------------------------------------

    token="$(
        curl \
            --noproxy '*' \
            -fsS \
            --max-time 2 \
            -X PUT \
            -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' \
            http://169.254.169.254/latest/api/token \
            2>/dev/null || true
    )"

    if [[ -n "$token" ]]; then

        public_ip="$(
            curl \
                --noproxy '*' \
                -fsS \
                --max-time 2 \
                -H "X-aws-ec2-metadata-token: ${token}" \
                http://169.254.169.254/latest/meta-data/public-ipv4 \
                2>/dev/null || true
        )"

        if valid_ipv4 "$public_ip"; then
            echo "$public_ip"
            return 0
        fi
    fi

    # --------------------------------------------------------
    # Generic fallback.
    # --------------------------------------------------------

    public_ip="$(
        curl \
            -4 \
            -fsS \
            --max-time 5 \
            https://api.ipify.org \
            2>/dev/null || true
    )"

    if valid_ipv4 "$public_ip"; then
        echo "$public_ip"
        return 0
    fi

    return 1
}

# ============================================================
# SERVER FIREWALL
# ============================================================

remove_server_rules() {
    local ext_if="${1:-}"

    [[ -n "$ext_if" ]] || return 0

    while iptables \
        -t nat \
        -C POSTROUTING \
        -s "$TUN_NET" \
        -o "$ext_if" \
        -m comment \
        --comment "$RULE_COMMENT" \
        -j MASQUERADE \
        2>/dev/null
    do
        iptables \
            -t nat \
            -D POSTROUTING \
            -s "$TUN_NET" \
            -o "$ext_if" \
            -m comment \
            --comment "$RULE_COMMENT" \
            -j MASQUERADE \
            2>/dev/null || true
    done

    while iptables \
        -C FORWARD \
        -i "$TUN_IF" \
        -o "$ext_if" \
        -m comment \
        --comment "$RULE_COMMENT" \
        -j ACCEPT \
        2>/dev/null
    do
        iptables \
            -D FORWARD \
            -i "$TUN_IF" \
            -o "$ext_if" \
            -m comment \
            --comment "$RULE_COMMENT" \
            -j ACCEPT \
            2>/dev/null || true
    done

    while iptables \
        -C FORWARD \
        -i "$ext_if" \
        -o "$TUN_IF" \
        -m conntrack \
        --ctstate RELATED,ESTABLISHED \
        -m comment \
        --comment "$RULE_COMMENT" \
        -j ACCEPT \
        2>/dev/null
    do
        iptables \
            -D FORWARD \
            -i "$ext_if" \
            -o "$TUN_IF" \
            -m conntrack \
            --ctstate RELATED,ESTABLISHED \
            -m comment \
            --comment "$RULE_COMMENT" \
            -j ACCEPT \
            2>/dev/null || true
    done
}


add_server_rules() {
    local ext_if="$1"

    remove_server_rules "$ext_if"

    iptables \
        -t nat \
        -A POSTROUTING \
        -s "$TUN_NET" \
        -o "$ext_if" \
        -m comment \
        --comment "$RULE_COMMENT" \
        -j MASQUERADE

    iptables \
        -A FORWARD \
        -i "$TUN_IF" \
        -o "$ext_if" \
        -m comment \
        --comment "$RULE_COMMENT" \
        -j ACCEPT

    iptables \
        -A FORWARD \
        -i "$ext_if" \
        -o "$TUN_IF" \
        -m conntrack \
        --ctstate RELATED,ESTABLISHED \
        -m comment \
        --comment "$RULE_COMMENT" \
        -j ACCEPT
}

# ============================================================
# SERVER STATE / CLEANUP
# ============================================================

server_cleanup_state() {
    local quiet="${1:-0}"

    [[ -f "$SERVER_STATE" ]] || return 0

    # shellcheck disable=SC1090
    source "$SERVER_STATE"

    if process_alive "${S_PID:-}"; then

        kill "$S_PID" 2>/dev/null || true

        for ((i=0; i<20; i++)); do
            process_alive "$S_PID" || break
            sleep 0.1
        done

        if process_alive "$S_PID"; then
            kill -9 "$S_PID" 2>/dev/null || true
        fi
    fi

    remove_server_rules "${S_EXT_IF:-}"

    if [[ -n "${S_ORIG_IP_FORWARD:-}" ]]; then
        sysctl \
            -w \
            "net.ipv4.ip_forward=${S_ORIG_IP_FORWARD}" \
            >/dev/null 2>&1 || true
    fi

    rm -f "$SERVER_STATE"

    if [[ "$quiet" != "1" ]]; then
        ok "DNS VPN server stopped."
    fi
}


server_monitor() {
    while [[ -f "$SERVER_STATE" ]]; do

        # shellcheck disable=SC1090
        source "$SERVER_STATE"

        if ! process_alive "${S_PID:-}"; then
            server_cleanup_state 1
            exit 0
        fi

        sleep 2
    done
}

# ============================================================
# SERVER STATUS
# ============================================================

server_status() {
    require_root
    check_dependencies server
    ensure_dirs

    heading "DNS VPN SERVER STATUS"

    if [[ ! -f "$SERVER_STATE" ]]; then
        fail "Server is not running."
        return 1
    fi

    # shellcheck disable=SC1090
    source "$SERVER_STATE"

    if ! process_alive "${S_PID:-}"; then
        fail "Server process is not running."
        warn "Stale state exists. Run: sudo dns-vpn server --stop"
        return 1
    fi

    ok "Server is running."

    echo
    printf "  %-18s %s\n" "PID:" "$S_PID"
    printf "  %-18s %s\n" "Mode:" "${S_MODE:-unknown}"
    printf "  %-18s %s\n" "Public IP:" "${S_PUBLIC_IP:-unknown}"
    printf "  %-18s %s\n" "Egress:" "${S_EXT_IF:-unknown}"
    printf "  %-18s %s\n" "Tunnel:" "$TUN_SERVER_CIDR"
    printf "  %-18s %s\n" "Top domain:" "$TOPDOMAIN"
    printf "  %-18s %s\n" "Log:" "$SERVER_LOG"

    echo

    if ip -4 addr show "$TUN_IF" >/dev/null 2>&1; then
        ok "$TUN_IF is active."
    else
        warn "$TUN_IF is not present."
    fi
}

# ============================================================
# SERVER START
# ============================================================

server_start() {
    local password="$1"
    local mode="$2"

    require_root
    check_dependencies server
    ensure_dirs

    if [[ -f "$SERVER_STATE" ]]; then

        # shellcheck disable=SC1090
        source "$SERVER_STATE"

        if process_alive "${S_PID:-}"; then
            die "A dns-vpn server is already running. Use --status or --stop."
        fi

        warn "Removing stale server state."
        server_cleanup_state 1
    fi

    heading "DNS VPN SERVER"

    local ext_if
    local original_forward
    local public_ip
    local pid

    ext_if="$(
        ip -4 route show default |
        awk '/default/ {print $5; exit}'
    )"

    [[ -n "$ext_if" ]] ||
        die "Could not determine the Internet-facing interface."

    public_ip="$(detect_public_ip || true)"

    if [[ -z "$public_ip" ]]; then
        die "Could not determine the server's public IPv4 address."
    fi

    original_forward="$(
        sysctl -n net.ipv4.ip_forward
    )"

    echo -e "  ${BOLD}Public IP:${RESET}       $public_ip"
    echo -e "  ${BOLD}Egress:${RESET}          $ext_if"
    echo -e "  ${BOLD}Tunnel:${RESET}          $TUN_SERVER_CIDR"
    echo -e "  ${BOLD}UDP Listener:${RESET}    53"
    echo

    info "Enabling IPv4 forwarding..."

    sysctl \
        -w \
        net.ipv4.ip_forward=1 \
        >/dev/null

    info "Configuring tunnel NAT..."

    add_server_rules "$ext_if"

    : > "$SERVER_LOG"
    chmod 600 "$SERVER_LOG"

    info "Starting iodined..."

    nohup iodined \
        -f \
        -P "$password" \
        "$TUN_SERVER_CIDR" \
        "$TOPDOMAIN" \
        >"$SERVER_LOG" \
        2>&1 \
        </dev/null &

    pid=$!

    local ready=0

    for ((i=0; i<40; i++)); do

        if ! process_alive "$pid"; then
            break
        fi

        if ip -4 addr show "$TUN_IF" >/dev/null 2>&1 &&
           grep -q "Listening to dns for domain" "$SERVER_LOG" 2>/dev/null
        then
            ready=1
            break
        fi

        sleep 0.25
    done

    if [[ "$ready" -ne 1 ]]; then

        kill "$pid" 2>/dev/null || true

        remove_server_rules "$ext_if"

        sysctl \
            -w \
            "net.ipv4.ip_forward=${original_forward}" \
            >/dev/null 2>&1 || true

        echo
        fail "iodined failed to start."

        echo
        tail -n 20 "$SERVER_LOG" 2>/dev/null || true

        exit 1
    fi

    {
        printf 'S_PID=%q\n' "$pid"
        printf 'S_EXT_IF=%q\n' "$ext_if"
        printf 'S_PUBLIC_IP=%q\n' "$public_ip"
        printf 'S_ORIG_IP_FORWARD=%q\n' "$original_forward"
        printf 'S_MODE=%q\n' "$mode"
    } > "$SERVER_STATE"

    chmod 600 "$SERVER_STATE"

    echo
    ok "DNS VPN server is running."

    echo
    printf "  %-18s %s\n" "PID:" "$pid"
    printf "  %-18s %s\n" "Public IP:" "$public_ip"
    printf "  %-18s %s\n" "Tunnel gateway:" "$TUN_SERVER_IP"
    printf "  %-18s %s\n" "Transport:" "UDP/53"
    printf "  %-18s %s\n" "Log:" "$SERVER_LOG"

    echo
    echo -e "${GREEN}${BOLD}Client connection command:${RESET}"
    echo

    printf '  sudo dns-vpn client -p %q -s %s\n' \
        "$password" \
        "$public_ip"

    echo

    if [[ "$mode" == "background" ]]; then

        nohup "$SCRIPT_SELF" \
            __server-monitor \
            >/dev/null \
            2>&1 \
            </dev/null &

        ok "Server is running in the background."
        info "Check with: sudo dns-vpn server --status"
        info "Stop with:  sudo dns-vpn server --stop"

        return 0
    fi

    info "Foreground mode."
    info "Press Ctrl+C to stop the server."

    trap '
        echo
        info "Stopping DNS VPN server..."
        server_cleanup_state 1
        ok "Server stopped."
        exit 0
    ' INT TERM HUP

    wait "$pid" || true

    server_cleanup_state 1

    warn "iodined exited."
}

# ============================================================
# SERVER ARGUMENT PARSING
# ============================================================

server_main() {
    local password=""
    local mode="foreground"
    local action="start"

    while [[ $# -gt 0 ]]; do
        case "$1" in

            -p|--password)
                [[ $# -ge 2 ]] || die "Missing value for $1."
                password="$2"
                shift 2
                ;;

            -b|--background)
                mode="background"
                shift
                ;;

            -f|--foreground)
                mode="foreground"
                shift
                ;;

            --status)
                action="status"
                shift
                ;;

            --stop)
                action="stop"
                shift
                ;;

            -h|--help)
                server_help
                exit 0
                ;;

            *)
                die "Unknown server option: $1"
                ;;
        esac
    done

    case "$action" in

        status)
            server_status
            ;;

        stop)
            require_root
            check_dependencies server
            ensure_dirs

            heading "STOP DNS VPN SERVER"

            if [[ ! -f "$SERVER_STATE" ]]; then
                warn "No managed DNS VPN server is running."
                exit 0
            fi

            server_cleanup_state 0
            ;;

        start)
            [[ -n "$password" ]] ||
                die "Server password required. Use: -p PASSWORD"

            server_start "$password" "$mode"
            ;;
    esac
}

# ============================================================
# CLIENT DNS DISCOVERY
# ============================================================

discover_dns_servers() {
    local interface="$1"
    local candidates=()

    if command -v nmcli >/dev/null 2>&1; then

        while IFS= read -r value; do

            value="${value//,/ }"

            for ipaddr in $value; do
                valid_ipv4 "$ipaddr" &&
                    [[ "$ipaddr" != 127.* ]] &&
                    candidates+=("$ipaddr")
            done

        done < <(
            nmcli \
                -g IP4.DNS \
                dev show "$interface" \
                2>/dev/null || true
        )
    fi

    if command -v resolvectl >/dev/null 2>&1; then

        while IFS= read -r ipaddr; do

            valid_ipv4 "$ipaddr" &&
                [[ "$ipaddr" != 127.* ]] &&
                candidates+=("$ipaddr")

        done < <(
            resolvectl dns "$interface" 2>/dev/null |
            grep -oE '([0-9]{1,3}\.){3}[0-9]{1,3}' || true
        )
    fi

    if [[ -r /etc/resolv.conf ]]; then

        while read -r _ ipaddr; do

            valid_ipv4 "$ipaddr" &&
                [[ "$ipaddr" != 127.* ]] &&
                candidates+=("$ipaddr")

        done < <(
            awk \
                '$1=="nameserver" {print $1,$2}' \
                /etc/resolv.conf
        )
    fi

    printf '%s\n' "${candidates[@]:-}" |
        grep -v '^$' |
        sort -u
}


add_physical_host_route() {
    local address="$1"
    local route
    local route_dev
    local route_gw

    route="$(
        ip -4 route get "$address" |
        head -1
    )"

    route_dev="$(
        awk '{
            for(i=1;i<=NF;i++)
                if($i=="dev") print $(i+1)
        }' <<<"$route"
    )"

    route_gw="$(
        awk '{
            for(i=1;i<=NF;i++)
                if($i=="via") print $(i+1)
        }' <<<"$route"
    )"

    [[ -n "$route_dev" ]] || return 1

    if [[ -n "$route_gw" ]]; then
        ip route add \
            "$address/32" \
            via "$route_gw" \
            dev "$route_dev"
    else
        ip route add \
            "$address/32" \
            dev "$route_dev"
    fi
}

# ============================================================
# CLIENT CLEANUP / MONITOR
# ============================================================

client_cleanup_state() {
    local quiet="${1:-0}"

    [[ -f "$CLIENT_STATE" ]] || return 0

    # shellcheck disable=SC1090
    source "$CLIENT_STATE"

    # Remove tunnel default BEFORE killing iodine.
    ip route del \
        default \
        via "$TUN_SERVER_IP" \
        dev "$TUN_IF" \
        metric 5 \
        2>/dev/null || true

    if [[ -n "${C_DNS_ROUTES:-}" ]]; then

        local dns_routes=()

        IFS=',' read -r -a dns_routes <<<"$C_DNS_ROUTES"

        for dns in "${dns_routes[@]}"; do
            [[ -n "$dns" ]] || continue

            ip route del \
                "$dns/32" \
                2>/dev/null || true
        done
    fi

    # Restore the original server-specific route if one existed.
    ip route del \
        "${C_SERVER_IP}/32" \
        2>/dev/null || true

    if [[ -n "${C_PREVIOUS_SERVER_ROUTE:-}" ]]; then
        # Route strings are generated by `ip route`, not user input.
        # shellcheck disable=SC2086
        ip route add $C_PREVIOUS_SERVER_ROUTE \
            2>/dev/null || true
    fi

    if process_alive "${C_PID:-}"; then

        kill "$C_PID" 2>/dev/null || true

        for ((i=0; i<20; i++)); do
            process_alive "$C_PID" || break
            sleep 0.1
        done

        if process_alive "$C_PID"; then
            kill -9 "$C_PID" 2>/dev/null || true
        fi
    fi

    rm -f "$CLIENT_STATE"

    if [[ "$quiet" != "1" ]]; then
        ok "DNS VPN disconnected."
        ok "Normal routing restored."
    fi
}


client_monitor() {
    while [[ -f "$CLIENT_STATE" ]]; do

        # shellcheck disable=SC1090
        source "$CLIENT_STATE"

        if ! process_alive "${C_PID:-}"; then
            client_cleanup_state 1
            exit 0
        fi

        sleep 2
    done
}

# ============================================================
# CLIENT STATUS
# ============================================================

client_status() {
    require_root
    check_dependencies client
    ensure_dirs

    heading "DNS VPN CLIENT STATUS"

    if [[ ! -f "$CLIENT_STATE" ]]; then
        fail "Client tunnel is not running."
        return 1
    fi

    # shellcheck disable=SC1090
    source "$CLIENT_STATE"

    if ! process_alive "${C_PID:-}"; then
        fail "iodine client process is not running."
        warn "Stale state exists. Run: sudo dns-vpn client --stop"
        return 1
    fi

    ok "DNS VPN client is connected."

    echo
    printf "  %-18s %s\n" "PID:" "$C_PID"
    printf "  %-18s %s\n" "Mode:" "${C_MODE:-unknown}"
    printf "  %-18s %s\n" "Server:" "$C_SERVER_IP"
    printf "  %-18s %s\n" "Physical:" "${C_PHYS_DEV:-unknown}"
    printf "  %-18s %s\n" "Tunnel:" "$TUN_IF"
    printf "  %-18s %s\n" "Gateway:" "$TUN_SERVER_IP"
    printf "  %-18s %s\n" "Log:" "$CLIENT_LOG"

    local tunnel_ip=""

    tunnel_ip="$(
        ip -4 -o addr show dev "$TUN_IF" 2>/dev/null |
        awk '{print $4}' |
        head -1
    )"

    if [[ -n "$tunnel_ip" ]]; then
        printf "  %-18s %s\n" "Tunnel IP:" "$tunnel_ip"
    fi

    echo

    local public_ip=""

    public_ip="$(
        curl \
            -4 \
            -fsS \
            --max-time 8 \
            https://api.ipify.org \
            2>/dev/null || true
    )"

    if valid_ipv4 "$public_ip"; then
        ok "Current public IP: $public_ip"
    else
        warn "Unable to determine current public IP."
    fi
}

# ============================================================
# CLIENT START
# ============================================================

client_start() {
    local password="$1"
    local server_ip="$2"
    local mode="$3"

    require_root
    check_dependencies client
    ensure_dirs

    valid_ipv4 "$server_ip" ||
        die "Invalid server IPv4 address: $server_ip"

    if [[ -f "$CLIENT_STATE" ]]; then

        # shellcheck disable=SC1090
        source "$CLIENT_STATE"

        if process_alive "${C_PID:-}"; then
            die "A DNS VPN client is already connected. Use --status or --stop."
        fi

        warn "Removing stale client state."
        client_cleanup_state 1
    fi

    if ip link show "$TUN_IF" >/dev/null 2>&1; then
        die "$TUN_IF already exists. Stop the existing iodine process first."
    fi

    heading "DNS VPN CLIENT"

    local route
    local phys_dev
    local phys_gw
    local previous_server_route
    local pid

    route="$(
        ip -4 route get "$server_ip" |
        head -1
    )"

    phys_dev="$(
        awk '{
            for(i=1;i<=NF;i++)
                if($i=="dev") print $(i+1)
        }' <<<"$route"
    )"

    phys_gw="$(
        awk '{
            for(i=1;i<=NF;i++)
                if($i=="via") print $(i+1)
        }' <<<"$route"
    )"

    [[ -n "$phys_dev" ]] ||
        die "Could not determine the physical route to $server_ip."

    if [[ -n "$phys_gw" ]]; then
        ok "Transport: $server_ip via $phys_gw dev $phys_dev"
    else
        ok "Transport: $server_ip directly via $phys_dev"
    fi

    previous_server_route="$(
        ip -4 route show exact "$server_ip/32" |
        head -1 || true
    )"

    # Always pin the iodine transport to the pre-existing network.
    if [[ -n "$phys_gw" ]]; then
        ip route replace \
            "$server_ip/32" \
            via "$phys_gw" \
            dev "$phys_dev"
    else
        ip route replace \
            "$server_ip/32" \
            dev "$phys_dev"
    fi

    # --------------------------------------------------------
    # Preserve existing DNS resolvers on the physical network.
    # --------------------------------------------------------

    local added_dns_routes=()
    local dns

    while IFS= read -r dns; do

        [[ -n "$dns" ]] || continue
        [[ "$dns" == "$server_ip" ]] && continue

        # Existing host route -> don't alter it.
        if ip -4 route show exact "$dns/32" |
            grep -q .
        then
            continue
        fi

        if add_physical_host_route "$dns" 2>/dev/null; then
            added_dns_routes+=("$dns")
        fi

    done < <(
        discover_dns_servers "$phys_dev"
    )

    : > "$CLIENT_LOG"
    chmod 600 "$CLIENT_LOG"

    echo
    info "Connecting to DNS VPN..."

    #
    # -r disables iodine's raw UDP optimisation.
    #
    # This ensures traffic stays in iodine's DNS transport mode.
    #
    nohup iodine \
        -f \
        -r \
        -P "$password" \
        "$server_ip" \
        "$TOPDOMAIN" \
        >"$CLIENT_LOG" \
        2>&1 \
        </dev/null &

    pid=$!

    printf "    Negotiating"

    local connected=0

    for ((i=0; i<90; i++)); do

        if grep -q \
            "Bad password" \
            "$CLIENT_LOG" \
            2>/dev/null
        then
            echo

            kill "$pid" 2>/dev/null || true

            echo
            tail -n 20 "$CLIENT_LOG" || true

            # Undo temporary routes.
            ip route del "$server_ip/32" 2>/dev/null || true

            if [[ -n "$previous_server_route" ]]; then
                # shellcheck disable=SC2086
                ip route add $previous_server_route \
                    2>/dev/null || true
            fi

            for dns in "${added_dns_routes[@]}"; do
                ip route del "$dns/32" 2>/dev/null || true
            done

            die "Server rejected the password."
        fi

        if grep -q \
            "Connection setup complete, transmitting data" \
            "$CLIENT_LOG" \
            2>/dev/null
        then
            connected=1
            break
        fi

        if ! process_alive "$pid"; then
            echo

            echo
            tail -n 30 "$CLIENT_LOG" || true

            ip route del "$server_ip/32" 2>/dev/null || true

            if [[ -n "$previous_server_route" ]]; then
                # shellcheck disable=SC2086
                ip route add $previous_server_route \
                    2>/dev/null || true
            fi

            for dns in "${added_dns_routes[@]}"; do
                ip route del "$dns/32" 2>/dev/null || true
            done

            die "iodine exited before the tunnel completed."
        fi

        printf "."
        sleep 1
    done

    echo

    if [[ "$connected" -ne 1 ]]; then

        kill "$pid" 2>/dev/null || true

        tail -n 30 "$CLIENT_LOG" || true

        ip route del "$server_ip/32" 2>/dev/null || true

        if [[ -n "$previous_server_route" ]]; then
            # shellcheck disable=SC2086
            ip route add $previous_server_route \
                2>/dev/null || true
        fi

        for dns in "${added_dns_routes[@]}"; do
            ip route del "$dns/32" 2>/dev/null || true
        done

        die "Timed out waiting for iodine negotiation."
    fi

    local tunnel_ip=""

    tunnel_ip="$(
        ip -4 -o addr show dev "$TUN_IF" 2>/dev/null |
        awk '{print $4}' |
        head -1
    )"

    [[ -n "$tunnel_ip" ]] ||
        die "iodine connected but $TUN_IF has no IPv4 address."

    ok "DNS tunnel connected."

    echo
    printf "  %-18s %s\n" "Tunnel IP:" "$tunnel_ip"
    printf "  %-18s %s\n" "Gateway:" "$TUN_SERVER_IP"
    printf "  %-18s %s\n" "Transport:" "${server_ip}:53/UDP"
    printf "  %-18s %s\n" "Physical:" "$phys_dev"

    if command -v ping >/dev/null 2>&1; then

        info "Checking tunnel endpoint..."

        if ping \
            -q \
            -c 1 \
            -W 5 \
            "$TUN_SERVER_IP" \
            >/dev/null 2>&1
        then
            ok "Tunnel endpoint reachable."
        else
            warn "Tunnel negotiated, but gateway did not answer ICMP."
        fi
    fi

    # --------------------------------------------------------
    # Prefer the VPN default.
    #
    # Existing DHCP/default route remains untouched.
    # --------------------------------------------------------

    info "Routing Internet traffic through $TUN_IF..."

    ip route add \
        default \
        via "$TUN_SERVER_IP" \
        dev "$TUN_IF" \
        metric 5

    local dns_csv=""

    if [[ ${#added_dns_routes[@]} -gt 0 ]]; then
        dns_csv="$(
            IFS=,
            echo "${added_dns_routes[*]}"
        )"
    fi

    {
        printf 'C_PID=%q\n' "$pid"
        printf 'C_MODE=%q\n' "$mode"
        printf 'C_SERVER_IP=%q\n' "$server_ip"
        printf 'C_PHYS_DEV=%q\n' "$phys_dev"
        printf 'C_PHYS_GW=%q\n' "$phys_gw"
        printf 'C_PREVIOUS_SERVER_ROUTE=%q\n' "$previous_server_route"
        printf 'C_DNS_ROUTES=%q\n' "$dns_csv"
    } > "$CLIENT_STATE"

    chmod 600 "$CLIENT_STATE"

    echo
    echo -e "${GREEN}${BOLD}============================================================${RESET}"
    echo -e "${GREEN}${BOLD}                 DNS VPN CONNECTED${RESET}"
    echo -e "${GREEN}${BOLD}============================================================${RESET}"
    echo

    ip -4 route show default |
        sed 's/^/  /'

    echo
    info "Testing Internet access..."

    local public_ip=""

    public_ip="$(
        curl \
            -4 \
            -fsS \
            --max-time 30 \
            https://api.ipify.org \
            2>/dev/null || true
    )"

    if valid_ipv4 "$public_ip"; then
        ok "Internet routing working."
        ok "Public IP: $public_ip"
    else
        warn "Tunnel is connected but the Internet test failed."
    fi

    echo

    if [[ "$mode" == "background" ]]; then

        nohup "$SCRIPT_SELF" \
            __client-monitor \
            >/dev/null \
            2>&1 \
            </dev/null &

        ok "DNS VPN is running in the background."
        info "Check with: sudo dns-vpn client --status"
        info "Stop with:  sudo dns-vpn client --stop"

        return 0
    fi

    info "Foreground mode."
    info "Press Ctrl+C to disconnect."

    trap '
        echo
        info "Disconnecting DNS VPN..."
        client_cleanup_state 1
        ok "Disconnected."
        ok "Normal routing restored."
        exit 0
    ' INT TERM HUP

    wait "$pid" || true

    client_cleanup_state 1

    warn "iodine exited."
    ok "Normal routing restored."
}

# ============================================================
# CLIENT ARGUMENT PARSING
# ============================================================

client_main() {
    local password=""
    local server_ip=""
    local mode="foreground"
    local action="start"

    while [[ $# -gt 0 ]]; do
        case "$1" in

            -p|--password)
                [[ $# -ge 2 ]] || die "Missing value for $1."
                password="$2"
                shift 2
                ;;

            -s|--server)
                [[ $# -ge 2 ]] || die "Missing value for $1."
                server_ip="$2"
                shift 2
                ;;

            -b|--background)
                mode="background"
                shift
                ;;

            -f|--foreground)
                mode="foreground"
                shift
                ;;

            --status)
                action="status"
                shift
                ;;

            --stop)
                action="stop"
                shift
                ;;

            -h|--help)
                client_help
                exit 0
                ;;

            *)
                die "Unknown client option: $1"
                ;;
        esac
    done

    case "$action" in

        status)
            client_status
            ;;

        stop)
            require_root
            check_dependencies client
            ensure_dirs

            heading "STOP DNS VPN CLIENT"

            if [[ ! -f "$CLIENT_STATE" ]]; then
                warn "No managed DNS VPN client is running."
                exit 0
            fi

            client_cleanup_state 0
            ;;

        start)
            [[ -n "$password" ]] ||
                die "Client password required. Use: -p PASSWORD"

            [[ -n "$server_ip" ]] ||
                die "Server IP required. Use: -s SERVER_IP"

            client_start \
                "$password" \
                "$server_ip" \
                "$mode"
            ;;
    esac
}

# ============================================================
# INTERNAL BACKGROUND MONITORS
# ============================================================

case "${1:-}" in

    __server-monitor)
        require_root
        check_dependencies server
        ensure_dirs
        server_monitor
        exit 0
        ;;

    __client-monitor)
        require_root
        check_dependencies client
        ensure_dirs
        client_monitor
        exit 0
        ;;
esac

# ============================================================
# MAIN
# ============================================================

case "${1:-}" in

    server)
        shift
        server_main "$@"
        ;;

    client)
        shift
        client_main "$@"
        ;;

    -h|--help|"")
        top_help
        ;;

    *)
        fail "Unknown command: $1"
        echo
        top_help
        exit 1
        ;;
esac