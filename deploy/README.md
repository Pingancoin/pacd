# PAC Mainnet Deployment

This directory holds the first production-facing deployment templates for `pacd`.

## Roles

Recommended first-pass mainnet roles:

1. `server1.pingancoin.org`
   - public `pacd`
   - DNS seed
   - public P2P
2. `server2.pingancoin.org`
   - public `pacd`
   - DNS seed
   - public P2P
3. `server3.pingancoin.org`
   - public `pacd`
   - DNS seed
   - wallet / pacdata upstream RPC
   - public P2P

Keep mining pool and wallet-facing services on separate hosts where possible.

## Ports

- `9508/tcp`: P2P mainnet
- `9509/tcp`: local HTTP RPC

Recommended exposure:

- P2P: public on seed/full-node servers
- RPC: bind to `127.0.0.1` unless a reverse proxy or private network is intentionally exposing it
- RPC auth: set `PACD_RPC_TOKEN` for any RPC service reachable outside the local host or a trusted private network

`pacd` refuses unauthenticated mainnet RPC on a non-loopback listen address
unless `--allowpublicrpc` is explicitly provided. Prefer keeping `PACD_RPC_LISTEN`
on `127.0.0.1:9509` and exposing only the needed public route through nginx.
If nginx fronts a token-protected local RPC, configure nginx to inject the
internal `Authorization: Bearer <token>` header and keep the token out of public
client configuration.

## Install

1. Build and copy `pacd` to `/usr/local/bin/pacd`
2. Create a service user:

```bash
sudo useradd --system --home /var/lib/pacd --shell /usr/sbin/nologin pacd
sudo mkdir -p /var/lib/pacd /etc/pingancoin
sudo chown -R pacd:pacd /var/lib/pacd
```

3. Copy:
   - [systemd/pacd-mainnet.service](/Users/fanye/Documents/pac/deploy/systemd/pacd-mainnet.service)
   - [pacd-mainnet.env.example](/Users/fanye/Documents/pac/deploy/pacd-mainnet.env.example)
   - optional static peer drop-in: [systemd/pacd-mainnet-static-peers.conf.example](/Users/fanye/Documents/pac/deploy/systemd/pacd-mainnet-static-peers.conf.example)
   - optional health timer:
     - [systemd/pacd-healthcheck.service](/Users/fanye/Documents/pac/deploy/systemd/pacd-healthcheck.service)
     - [systemd/pacd-healthcheck.timer](/Users/fanye/Documents/pac/deploy/systemd/pacd-healthcheck.timer)
     - [pacd-healthcheck.env.example](/Users/fanye/Documents/pac/deploy/pacd-healthcheck.env.example)
   - optional reverse proxy template: [nginx/pacd-rpc.conf.example](/Users/fanye/Documents/pac/deploy/nginx/pacd-rpc.conf.example)

4. Adjust `/etc/pingancoin/pacd-mainnet.env`
   - leave `PACD_RPC_LISTEN=127.0.0.1:9509` for normal deployments
   - set `PACD_RPC_TOKEN` when a reverse proxy or private service talks to RPC
5. Validate consensus readiness:

```bash
pacd launch-check --network mainnet
```

6. Start the node:

```bash
sudo systemctl daemon-reload
sudo systemctl enable pacd-mainnet
sudo systemctl start pacd-mainnet
sudo systemctl status pacd-mainnet
```

## Seed Node Static Peers

For the first three official seed nodes, prefer a systemd drop-in that connects
each node to the other two seed nodes explicitly. This makes the early network
less dependent on DNS/discovery timing while still keeping normal discovery
enabled.

Create `/etc/systemd/system/pacd-mainnet.service.d/static-peers.conf` from the
example and remove the local node from its own `--connect` list. Then reload and
restart:

```bash
sudo systemctl daemon-reload
sudo systemctl restart pacd-mainnet
```

## Health Checks

Install the health check script and timer:

```bash
sudo install -m 0755 scripts/pacd-health-check.sh /usr/local/bin/pacd-health-check
sudo install -m 0644 deploy/systemd/pacd-healthcheck.service /etc/systemd/system/pacd-healthcheck.service
sudo install -m 0644 deploy/systemd/pacd-healthcheck.timer /etc/systemd/system/pacd-healthcheck.timer
sudo cp deploy/pacd-healthcheck.env.example /etc/pingancoin/pacd-healthcheck.env
sudo systemctl daemon-reload
sudo systemctl enable --now pacd-healthcheck.timer
```

For seed nodes, set `PACD_HEALTH_MIN_PEERS=1` or higher. The timer logs failures
to journald and returns non-zero when the service is down, RPC is unavailable,
network name is wrong, height is below the configured minimum, or peer count is
too low.

## Pre-launch checklist

- final 3-of-5 project payout script inserted into mainnet params
- `pacd launch-check --network mainnet` returns ready
- DNS for `server1..server3.pingancoin.org` resolves correctly
- P2P port `9508/tcp` reachable from the public internet on seed nodes
- RPC port `9509/tcp` bound privately unless intentionally proxied
- static seed peer drop-ins are installed on official seed nodes
- health check timer is enabled on official seed nodes
- proxied RPC uses an internal bearer token or stays on a trusted private network
- `scripts/mainnet-release-check.sh` passes on the release commit
- release binaries built from a clean git tree

## Notes

- `launch-check` is intentionally strict about frozen mainnet consensus values
- `systemd` template uses a per-service data directory and local-only RPC by default
- nginx template shows a token-injecting `/rpc/` proxy for wallet/explorer use
- if startup reports a corrupt `blocks.jsonl`, stop the service and run once
  with `--repairstore`; pacd will backup the original file and truncate to the
  last fully validated block
- add reverse proxy, metrics, and log shipping separately; this directory is the first deployment baseline, not the final ops stack
