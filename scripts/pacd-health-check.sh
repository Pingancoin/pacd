#!/usr/bin/env bash
set -euo pipefail

SERVICE_NAME="${PACD_HEALTH_SERVICE:-pacd-mainnet}"
RPC_URL="${PACD_HEALTH_RPC_URL:-http://127.0.0.1:9509}"
EXPECTED_NETWORK="${PACD_HEALTH_NETWORK:-mainnet}"
MIN_PEERS="${PACD_HEALTH_MIN_PEERS:-1}"
MIN_HEIGHT="${PACD_HEALTH_MIN_HEIGHT:-0}"

if command -v systemctl >/dev/null 2>&1; then
  systemctl is-active --quiet "$SERVICE_NAME"
fi

network_info="$(curl --fail --silent --show-error --max-time 5 "$RPC_URL/getnetworkinfo")"
peer_info="$(curl --fail --silent --show-error --max-time 5 "$RPC_URL/getpeerinfo")"

python3 - "$EXPECTED_NETWORK" "$MIN_PEERS" "$MIN_HEIGHT" "$network_info" "$peer_info" <<'PY'
import json
import sys

expected_network = sys.argv[1]
min_peers = int(sys.argv[2])
min_height = int(sys.argv[3])
network_info = json.loads(sys.argv[4])
peer_info = json.loads(sys.argv[5])

errors = []
if network_info.get("network") != expected_network:
    errors.append(f"network={network_info.get('network')} expected={expected_network}")
height = int(network_info.get("bestheight", -1))
if height < min_height:
    errors.append(f"height={height} min={min_height}")
peers = int(network_info.get("peercount", -1))
peer_count = int(peer_info.get("count", -1))
if peers < min_peers:
    errors.append(f"peercount={peers} min={min_peers}")
if peer_count != peers:
    errors.append(f"peerinfo_count={peer_count} networkinfo_peercount={peers}")

if errors:
    print("pacd health check failed: " + "; ".join(errors), file=sys.stderr)
    sys.exit(1)

print(f"pacd health ok: network={expected_network} height={height} peers={peers}")
PY
