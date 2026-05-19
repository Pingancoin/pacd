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
   - wallet / pacdata upstream RPC
   - public P2P
4. `server4.pingancoin.org`
   - public `pacd`
   - DNS seed
   - public P2P

Keep mining pool and wallet-facing services on separate hosts where possible.

## Ports

- `9508/tcp`: P2P mainnet
- `9509/tcp`: local HTTP RPC

Recommended exposure:

- P2P: public on seed/full-node servers
- RPC: bind to `127.0.0.1` unless a reverse proxy or private network is intentionally exposing it

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

4. Adjust `/etc/pingancoin/pacd-mainnet.env`
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

## Pre-launch checklist

- final 3-of-5 project payout script inserted into mainnet params
- `pacd launch-check --network mainnet` returns ready
- DNS for `server1..server4.pingancoin.org` resolves correctly
- P2P port `9508/tcp` reachable from the public internet on seed nodes
- RPC port `9509/tcp` bound privately unless intentionally proxied
- `scripts/mainnet-release-check.sh` passes on the release commit
- release binaries built from a clean git tree

## Notes

- `launch-check` is intentionally strict about the placeholder project payout script
- `systemd` template uses a per-service data directory and local-only RPC by default
- add reverse proxy, metrics, and log shipping separately; this directory is the first deployment baseline, not the final ops stack
