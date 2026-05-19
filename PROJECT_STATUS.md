# Pingancoin Project Status

Last updated: 2026-05-19

## Repositories

| Repo | Purpose | Current state |
| --- | --- | --- |
| `pac` | chain core, node, P2P, mining RPC | mainnet hardening in progress |
| `pacdata` | indexer and read API | usable minimal indexer |
| `pacexplorer` | block explorer UI | production polish in progress |
| `pacpool` | pool control plane and Stratum | advancing toward payout-ready pool |
| `pacwallet` | standalone wallet stack | Go wallet backend plus native Qt desktop wallet direction |

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
- native `C++/Qt` desktop wallet scaffold under `pacwallet/qt`
- Qt 6 toolchain installed and native client compiling on macOS
- native macOS `.app` bundle build path plus release script
- native welcome/setup flow with create and restore
- native settings view now wires wallet security, upstream switching, private-key import, backups, and local service control
- native overview now shows wallet state plus UTXO inventory
- native transaction view now supports filtering, search, and detail drill-down
- native receive page now supports copy helpers and QR export
- native send page now supports spendable balance display, change-address selection, max helper, and confirm-before-send
- native multisig page now supports local export plus preview/result export
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
- `pacwallet`: roughly 80%
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
3. `pacwallet`: native Qt desktop wallet
   - complete overview / receive / send / transactions / multisig / settings views
   - connect to `pacwallet serve`
   - replace browser-hosted desktop shell with native Qt UI
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

`pacwallet` native Qt desktop wallet

Progress inside that line:

- completed: Go wallet backend and JSON API
- completed: browser-hosted desktop candidate and Windows packaging path
- completed: native Qt project scaffold with API client, service controller, and core wallet pages
- completed: Qt 6 toolchain install and first successful native app build
- completed: first-run welcome flow plus connected native settings / overview / transactions improvements
- completed: native receive/send/multisig ergonomics and macOS bundle release path
- next: keep driving the Qt client until it fully replaces the browser-hosted desktop flow for everyday wallet use
