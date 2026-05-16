# Pingancoin `pacd`

`pacd` is the first minimal chain-core milestone for Pingancoin / PAC.

The current repository is intentionally small: it establishes the PAC
consensus constants, BLAKE-256 proof-of-work hash path, block subsidy schedule,
95/5 coinbase split, and pure-PoW block validation before wallet, explorer,
pool, and full P2P/RPC surfaces are added.

## Consensus Draft

| Field | Value |
| --- | --- |
| Coin | Pingancoin |
| Ticker | PAC |
| Consensus | Pure PoW |
| PoW hash | BLAKE-256, 14 rounds |
| Target block time | 150 seconds |
| Initial subsidy | 16.92065961 PAC |
| Subsidy reduction | `subsidy = subsidy * 100 / 101` every 12,288 blocks |
| Max supply target | Approximately 21 million PAC |
| Coinbase split | 95% miner, 5% project development multisig |
| First normal block split | 16.07462662 PAC miner / 0.84603299 PAC project |
| Coinbase maturity | 100 blocks on mainnet/testnet, 2 blocks on simnet |
| Premine | 0 |
| Genesis time | 2026-06-01 00:00:00 UTC |
| Genesis message | `Pingancoin PAC genesis: pure PoW, no premine, BLAKE-256 r14, 2026-06-01` |

## Pure PoW Boundary

PAC deliberately removes Decred's hybrid PoW/PoS path. The minimal chain core
does not define ticket purchases, votes, stake validation, stake difficulty,
stakebase transactions, treasury voting, or agenda voting.

## Layout

```text
cmd/pacd              minimal node/miner CLI
cmd/pacwallet         encrypted developer wallet CLI
internal/blockchain   in-memory chain validation
internal/chaincfg     PAC network parameters
internal/consensus    subsidy, PoW, ASERT difficulty
internal/mining       candidate block and nonce search
internal/rpcserver    local HTTP RPC for blocks, mempool, mining, tx lookup
internal/wallet       wallet keys, balance scan, signing, submission
internal/wire         block and transaction primitives
docs/                 project design notes
```

## Try It

Use Go 1.25+:

```bash
go test ./...
go run ./cmd/pacd --network simnet --printparams
go run ./cmd/pacd --network simnet --mine PsimMiner --blocks 3
```

Generate a mainnet `P...` address from a compressed public key:

```bash
go run ./cmd/pacd address pubkey --network mainnet --pubkey <compressed-pubkey-hex>
```

Generate the project development 3-of-5 multisig P2SH address:

```bash
go run ./cmd/pacd address multisig --network mainnet --required 3 \
  --pubkey <pubkey1-hex> --pubkey <pubkey2-hex> --pubkey <pubkey3-hex> \
  --pubkey <pubkey4-hex> --pubkey <pubkey5-hex>
```

After the final redeem script is chosen, verify the mainnet consensus payout:

```bash
go run ./cmd/pacd address validate-project --redeemscript <redeem-script-hex>
```

Mainnet launch requires replacing the placeholder project payout script with
the final 3-of-5 multisig script generated from the project's five public keys.

## Wallet Preview

`pacwallet` can create encrypted local wallets, generate receiving addresses,
import/export private keys, export public keys for multisig setup, sign and
submit basic P2PKH transactions, track transaction history, and distinguish
spendable, immature, and pending balances. Wallet files are stored with `0600`
permissions. New wallets should use `--passphrase` or
`PACWALLET_PASSPHRASE`.

```bash
PACWALLET_PASSPHRASE='change-this-dev-passphrase' go run ./cmd/pacwallet create --network simnet
PACWALLET_PASSPHRASE='change-this-dev-passphrase' go run ./cmd/pacwallet info --network simnet
PACWALLET_PASSPHRASE='change-this-dev-passphrase' go run ./cmd/pacwallet receive --network simnet --label miner-1
go run ./cmd/pacwallet list --network simnet
go run ./cmd/pacwallet pubkeys --network simnet
go run ./cmd/pacwallet balance --network simnet --rpc http://127.0.0.1:9509
go run ./cmd/pacwallet history --network simnet --rpc http://127.0.0.1:9509
go run ./cmd/pacwallet drafttx --network simnet --rpc http://127.0.0.1:9509 --to <address> --amount 1.25
PACWALLET_PASSPHRASE='change-this-dev-passphrase' go run ./cmd/pacwallet drafttx --network simnet --rpc http://127.0.0.1:9509 --to <address> --amount 1.25 --sign
PACWALLET_PASSPHRASE='change-this-dev-passphrase' go run ./cmd/pacwallet send --network simnet --rpc http://127.0.0.1:9509 --to <address> --amount 1.25
```

Existing plaintext developer wallets are still readable for local testing, but
mainnet launch wallets should be created with encryption enabled from the
start.

`pacd` also exposes a minimal simnet transaction loop over HTTP RPC:

```bash
curl -s http://127.0.0.1:9509/getrawmempool
curl -s http://127.0.0.1:9509/getrawtransaction/<txid>
curl -s http://127.0.0.1:9509/getaddressutxos/<address>
curl -s -X POST http://127.0.0.1:9509/generate \
  -H 'content-type: application/json' \
  -d '{"address":"<simnet-miner-address>","blocks":1}'
```

## Supply Note

The initial subsidy is adjusted for PAC's 150-second block target and 12,288
block reduction interval so zero-premine issuance lands very close to 21
million PAC while keeping the smooth `100/101` reduction style. The current
integer subsidy schedule estimates total block subsidy at approximately
20,999,999.99721303 PAC.
