# PAC Consensus Notes

## Network Identity

- Name: Pingancoin
- Ticker: PAC
- Mainnet address family: `P...`
- Testnet address family: `T...`
- Simnet address family: `S...`

The address encoder is scheduled for the wallet milestone. The chain core
already treats the project payout as a consensus script, not a mutable runtime
configuration value. Mainnet P2PKH and P2SH addresses use Base58Check version
bytes selected to produce `P...` addresses.

## Genesis

- Timestamp: 2026-06-01 09:00:00 UTC
- Message: `Pingancoin PAC genesis: pure PoW, no premine, BLAKE-256 r14, 2026-06-01`
- Spendable premine: 0 PAC

The genesis block is a fixed anchor and does not create spendable PAC. Normal
subsidy starts at block 1.

## Subsidy

PAC follows the Decred-style smooth reduction schedule:

```text
base subsidy: 16.92065961 PAC
reduction interval: 12,288 blocks
multiplier/divisor: 100/101
```

Every normal block splits the base subsidy:

```text
95% miner
5% project development multisig
```

At block 1, that is:

```text
16.07462662 PAC miner
0.84603299 PAC project development multisig
```

Transaction fees are planned to go fully to the miner. That keeps the 5% project
stream limited to new issuance only.

### Supply

PAC adjusts the initial subsidy and reduction interval for a 150-second block
target:

```text
16.92065961 PAC initial subsidy
12,288 block reduction interval
100/101 smooth reduction
0 premine
approximately 21 million PAC total subsidy
```

This keeps the long-term emission curve closer to a time-based schedule instead
of making issuance twice as fast merely because PAC targets faster blocks.
The current integer subsidy estimate is approximately 20,999,999.99721303 PAC.

## Difficulty

PAC uses per-block ASERT-style difficulty adjustment instead of a daily or
large-window retarget. The initial implementation uses integer fixed-point
calculation, a 150-second target spacing, and a configurable half-life.
Mainnet starts from an initial proof-of-work difficulty of approximately 6 so
the first block can be found by early GPU miners before ASERT has live block
timing data.

## Removed Decred PoS Surface

PAC is pure PoW. The following Decred hybrid consensus surfaces are out of
scope for PAC and must not enter consensus validation:

- ticket purchase transactions
- live/missed ticket pools
- vote transactions
- stakebase transactions
- stake difficulty
- stake validation height
- agenda voting
- treasury voting
- PoS subsidy proportions

Future code reviews should treat accidental reintroduction of these concepts as
a consensus regression.
