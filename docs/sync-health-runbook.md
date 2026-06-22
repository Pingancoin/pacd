# Sync Health Runbook

This note records a production sync incident pattern and the public-safe fixes
that prevent it from recurring. Do not add host IP addresses, credentials,
wallet seeds, admin tokens, or private deployment paths to this file.

## Symptoms

- Explorer/read API height lagged behind the pool height.
- The pool node continued to advance, while another public node stayed on an
  older block height.
- Read API requests could become slow or time out while the indexer was catching
  up.
- Pool `/status` responses could become too large after many solved rounds.
- Node logs could show repeated self-dial attempts and temporary peer-slot
  pressure.

## Root Causes

- P2P sync requested only a partial block batch for a full headers batch and did
  not periodically retry `getheaders` while already connected to a higher peer.
- Indexer sync held its write lock while fetching and saving many blocks, which
  blocked `/status` and other read endpoints during catch-up.
- The pool status endpoint returned a large solved-round history in the main
  status payload.
- Self-connections were rejected by nonce during handshake, but the discovered
  self address was not remembered and skipped on later dials.

## Fixes

- `pacd` now periodically asks higher peers for headers, requests the full
  advertised header batch, and avoids per-block `getheaders` floods.
- `pacd` tracks addresses proven to be self-addresses and skips them during
  reservation, static reconnect loops, and discovery.
- `pacdata` keeps API reads responsive during catch-up by shortening lock scope
  and saving snapshots in batches instead of after every block.
- `pacpool` trims the main `/status` solved-round list to a small recent window.
  Full payout/accounting state remains internal and is not changed by the public
  status trim.

## Verification

Use public service names, not private host addresses, when documenting checks.

```bash
curl -sSf https://api.pingancoin.org/status
curl -sSf https://explorer.pingancoin.org/api/status
curl -sSf https://pool.pingancoin.org/status
```

Confirm that the read API, explorer API, and pool API report the same indexed
height and block hash. For the pool, compare both its node block height/hash and
its indexer height/hash.

For local development, run the affected test suites before release:

```bash
go test ./...
```

Run this in each changed repository.
