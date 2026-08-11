# cosmos-light-go

Replays Cosmos consensus and state proofs offline, from chain data captured once
and frozen.

It answers two questions about that captured data, from the bytes alone:

- **Did more than two thirds of the validator set's _voting power_ sign this
  header?** Not two thirds of the validators. On the captured set those are very
  different numbers, and the fixture makes the difference visible rather than
  theoretical — see below.
- **Does this key hold this value in the application state that header commits
  to?** That is two ICS-23 proofs chained, not one.

A third entry point answers the same question in the negative: that a key is
**absent** from that committed state.

```go
f, err := cosmoslight.Load()
if err != nil {
    return err
}

res, err := f.VerifyCommit()      // signatures + voting power
if err != nil {
    return err
}
fmt.Println(res.SignedCount, "of", res.ValidatorCount, "validators;",
    res.SignedVotingPower, "of", res.TotalVotingPower, "voting power")

if err := f.VerifyMembership(); err != nil {    // key is present, with this value
    return err
}
if err := f.VerifyNonMembership(); err != nil { // key is absent
    return err
}
```

## What this is not

**This is not a light client.** A light client follows a chain: it holds a
trusted header, requests new ones, checks them against a trust period and a
trust level, and updates what it trusts. Nothing here does any of that. There is
no trust period, no bisection, no header store, and no network code of any kind
at verification time.

The only code in this repository that opens a socket is the capture test, which
is skipped unless `COSMOS_LIGHT_CAPTURE=1` — and being a `_test.go` file, it is
never compiled into a consumer's binary at all. A test asserts that no
network-capable package is imported outside it, so that stays true.

The fixtures under `testdata/` are frozen at the height named below. They do not
advance and are not meant to. That is a design property rather than a
shortcoming: a verifier whose inputs are fixed is a verifier whose failures are
reproducible, which is what lets the planted-mutation matrix flip one bit in one
signature and assert the exact rejection. You cannot do that against a live
endpoint.

## The captured data

| | |
|---|---|
| Network | `osmo-test-5` (public Osmosis test network) |
| Endpoint | `https://rpc.osmotest5.osmosis.zone` |
| CometBFT | 0.38.17 |
| Header height | 68199363 |
| State read at height | 68199362 |
| Captured (UTC) | 2026-08-11T18:56:17Z |
| Module store | `bank` |

The state is read at one height and committed by the header at the **next** one:
a Cosmos header's `AppHash` is the root of the state the previous block left
behind. Both heights are recorded in `testdata/capture.json`, and the capture
asserts the relationship rather than assuming it.

**The endpoint was resolved from the [Cosmos chain
registry](https://github.com/cosmos/chain-registry) and confirmed serving before
it was recorded.** It is a default, not a requirement — `COSMOS_LIGHT_RPC`
points a re-capture anywhere.

### Voting power is not validator count

The captured set has five validators, and two of them hold 99.4% of the power:

| Voting power | Share |
|---:|---:|
| 43,980,086 | 51.32% |
| 41,224,130 | 48.11% |
| 208,362 | 0.24% |
| 203,101 | 0.24% |
| 79,387 | 0.09% |

So a **minority of validators** (2 of 5) can carry the commit, while a
**majority of validators** (3 of 5 — the three small ones) carries 0.57% of the
power and cannot. Both directions are asserted in the tests. A verifier that
counted validators instead of power would accept the second set, and no
happy-path test would ever notice.

The threshold is a **strict** inequality: CometBFT computes
`needed = total * 2 / 3` in integer arithmetic and rejects when
`tallied <= needed`. Exactly two thirds is not enough.

## Planted failures

A verifier that cannot go red is not a verifier. `plants_test.go` applies
byte-level mutations to in-memory copies of the real fixtures — the files on
disk are never touched — and asserts the exact outcome of each. Run
`go test -run TestPlantMatrix -v ./...` to print the table.

## Re-capturing

```
COSMOS_LIGHT_CAPTURE=1 go test -run TestCapture -v ./...
```

This **replaces every file under `testdata/`** and therefore invalidates every
height, hash and figure quoted above and in any consumer. It is a deliberate
act. The capture verifies its own output in a staging directory and refuses to
promote anything that does not check out, so a broken capture cannot quietly
replace good fixtures.

## Dependencies

Two, on purpose:

- `github.com/cometbft/cometbft` — the consensus types, signature scheme and
  vote sign-bytes
- `github.com/cosmos/ics23/go` — the proof specs and verification

**No `cosmos-sdk`, no `cosmossdk.io/store`, no `cosmos-db`.** That boundary is
the point: the full SDK triples the module graph and pulls in a peer-to-peer
stack, a set of embedded databases and a telemetry exporter suite, none of which
verifying a proof requires.

## Licence

MIT for the Go source. The captured chain data under `testdata/` is consensus
output from a public test network, reproduced verbatim so the verification can
be replayed; see `LICENSE` and `testdata/README.md`.
