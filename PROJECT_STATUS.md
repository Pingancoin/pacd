# Pingancoin Project Status

Last updated: 2026-05-18

## Repositories

| Repo | Purpose | Current state |
| --- | --- | --- |
| `pac` | chain core, node, P2P, mining RPC | usable developer chain core |
| `pacdata` | indexer and read API | usable minimal indexer |
| `pacexplorer` | block explorer UI | usable minimal explorer |
| `pacpool` | pool control plane and Stratum | advancing toward payout-ready pool |
| `pacwallet` | standalone CLI wallet | usable developer wallet CLI |

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
   - desktop or web UI
   - backup / restore UX
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

`pacpool` payout groundwork

Progress inside that line:

- completed: round archiving model
- completed: found-block attribution model
- completed: round share-weight accounting
- completed: payout calculation preview per round
- completed: miner balances and payout execution ledger
- next: miner dashboard API and wallet-linked payout automation
