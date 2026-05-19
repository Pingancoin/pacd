# Pingancoin Project Status

Last updated: 2026-05-19

## Repositories

| Repo | Purpose | Current state |
| --- | --- | --- |
| `pac` | chain core, node, P2P, mining RPC | mainnet hardening in progress |
| `pacdata` | indexer and read API | usable minimal indexer |
| `pacexplorer` | block explorer UI | production polish in progress |
| `pacpool` | pool control plane and Stratum | advancing toward payout-ready pool |
| `pacwallet` | standalone wallet stack | Bitcoin-style desktop wallet V1 candidate with service/UI and Windows release flow |

## What Works Now

### `pac`
- Pure PoW BLAKE-256 chain rules
- ASERT-style difficulty adjustment
- P2P handshake, header-first sync, block relay, tx relay
- mempool
- `getblocktemplate` / `submitblock`
- simnet mining and multi-node sync
- mainnet launch-check command
- first systemd deployment template
- release-blocking mainnet preflight script

### `pacdata`
- chain indexing from `pacd`
- block / tx / address read API
- pagination
- continuous tip tracking

### `pacexplorer`
- home page
- block page
- tx page
- address page
- paging wired to `pacdata`
- optional address labels
- label-based search
- short-TTL in-memory caching
- real upstream health check endpoint

### `pacpool`
- Stratum subscribe / authorize / notify / submit
- share validation
- solved block submission to `pacd`
- fixed base difficulty plus per-worker VarDiff
- persisted share ledger
- persisted worker stats

### `pacwallet`
- encrypted local wallet
- receive address generation
- balance and history scan
- signing and sending basic transactions
- local wallet service and JSON API
- browser UI wallet
- desktop launcher for Windows-style app windows
- desktop dashboard with summary, node health, send/receive, UTXO table, history table, multisig preview, encryption, and backup controls
- receive QR rendering
- transaction detail drill-down pages
- history filters and txid/address search
- public key export and 3-of-5 multisig preview flow
- backup restore flow with archived wallet snapshots
- Windows release directory build script
- upstream RPC endpoint profiles with local-first switching
- desktop release metadata, config templates, and zipped Windows bundle
- generated branding assets and first-run desktop onboarding polish
- desktop auto-import of official RPC presets from release templates
- installer now targets per-user program/config paths with Windows-native build and signing helpers

## Current Completion View

- `pac`: roughly 75%
- `pacdata`: roughly 70%
- `pacexplorer`: roughly 65%
- `pacpool`: roughly 65%
- `pacwallet`: roughly 78%
- full production-ready stack: roughly 55%

## Ordered Next Steps

This is the planned build order from here. Unless priorities change, continue in this order:

1. `pacpool`: payout groundwork
   - round tracking
   - solved block attribution
   - share weighting per round
   - payout calculation model
2. `pacpool`: payout engine
   - miner balances
   - payment records
   - payout execution flow
3. `pacwallet`: desktop wallet final polish
   - multi-step multisig coordination and signing
   - optional QR amount presets and payment request flow
   - final desktop distribution polish
4. `pacexplorer`: production polish
   - richer stats
   - better search and labels
   - deployment / cache / ops polish
5. `pac`: mainnet hardening
   - final mainnet constants
   - final project multisig script
   - deployment, monitoring, release flow

## Immediate Active Work

The currently active line is:

`pacwallet` V1 completion and release packaging

Progress inside that line:

- completed: wallet service, JSON API, and Windows desktop launcher
- completed: first-run onboarding and upstream profile flow
- completed: desktop dashboard with send/receive, UTXO/history, backup, encryption, multisig preview, and pubkey export
- completed: receive QR flow and transaction detail pages
- next: final smoke pass, Windows package rebuild, and single V1 candidate release
