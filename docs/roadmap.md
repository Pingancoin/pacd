# PAC Build Roadmap

## Phase 1: `pacd` Minimal Chain Core

- Define PAC consensus parameters.
- Implement BLAKE-256 r14 header hashing.
- Implement pure-PoW block validation.
- Implement DCR-style smooth subsidy reduction.
- Enforce 95% miner / 5% project multisig coinbase split.
- Implement per-block ASERT-style difficulty.
- Provide simnet mining CLI for local validation.

## Phase 2: Wallet

- Add address encoding with mainnet `P...` prefix.
- Generate final 3-of-5 project multisig script from five public keys.
- Build `pacwallet` key generation, address listing, transaction creation, and signing.
- Add signed transaction submission through the local `pacd` RPC.
- Encrypt wallet private keys with passphrase-based AES-GCM storage.
- Add private key import/export and passphrase rotation.
- Add coinbase maturity, wallet balance indexing, and transaction history.
- Split `pacwallet` into `Pingancoin/pacwallet` as a standalone wallet
  repository.

## Phase 3: Full Node Surfaces

- Add persistent block database.
- Add minimal mempool and UTXO validation.
- Add RPC methods for mining, block lookup, transaction broadcast, and node
  status.
- Add wallet-facing transaction and address UTXO lookup RPC methods.
- Add P2P peer management, version/verack handshake, ping/pong, and DNS seeds.
- Add header-first sync and block relay.
- Add inventory relay, peer address gossip, ban scores, orphan handling, and
  parallel block download.

## Phase 4: Explorer

- Build `pacdata` indexer.
- Add block, transaction, address, rich list, and miner pages.
- Add public API for pool and wallet integrations.

## Phase 5: Official Pool

- Build `pacpool`.
- Add stratum support for BLAKE-256 r14 ASIC miners.
- Add payout accounting, dashboard, and pool fee configuration.
