# Pingancoin Project Status

Last updated: 2026-05-19

## Repositories

| Repo | Purpose | Current state |
| --- | --- | --- |
| `pac` | chain core, node, P2P, mining RPC | usable developer chain core |
| `pacdata` | indexer and read API | usable minimal indexer |
| `pacexplorer` | block explorer UI | usable minimal explorer |
| `pacpool` | pool control plane and Stratum | advancing toward payout-ready pool |
| `pacwallet` | standalone wallet stack | CLI wallet plus service/UI, desktop launcher with release candidate flow |

## What Works Now

### `pac`
- Pure PoW BLAKE-256 chain rules
- ASERT-style difficulty adjustment
- P2P handshake, header-first sync, block relay, tx relay
- mempool
- `getblocktemplate` / `submitblock`
- simnet mining and multi-node sync

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
- `pacwallet`: roughly 45%
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
3. `pacwallet`: wallet service and UI wallet
   - wallet daemon / RPC
   - web UI
   - Windows desktop launcher
   - backup / restore UX polish
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

`pacwallet` service and desktop wallet

Progress inside that line:

- completed: wallet service layer over local wallet core
- completed: JSON API and browser wallet UI
- completed: desktop launcher skeleton for Windows app-window use
- completed: backup restore flow with auto-archived wallet snapshots
- completed: Windows release packaging script and launcher files
- completed: upstream RPC endpoint profile management
- completed: desktop release metadata and config-driven Windows bundle
- completed: generated app icon set, installer/signing scaffolding, and first-run onboarding polish
- completed: desktop auto-import of official RPC presets from bundled templates
- completed: installer path cleanup for per-user Windows installs and release helper bat files
- next: updater path, certificate-backed signing, and wallet UX hardening
