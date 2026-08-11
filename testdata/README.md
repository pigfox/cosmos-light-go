# Captured chain data

Every file here was written by `TestCapture` in one run and must be treated as a
set: the header, the validator sets and the two proofs are all from the same
capture and only verify against each other.

**Do not hand-edit these files.** Re-capture instead:

```
COSMOS_LIGHT_CAPTURE=1 go test -run TestCapture -v ./...
```

`capture.json` is the provenance record — endpoint, chain ID, CometBFT version,
capture time, and the three heights involved. It is written by the same run that
writes everything else, so it cannot drift from the data it describes.

| File | What it is |
|---|---|
| `capture.json` | Provenance: endpoint, chain ID, node version, capture time, heights |
| `signed_header.json` | The header at `header_height` and the commit signing it |
| `validators.json` | The validator set in force at `header_height` |
| `foreign_validators.json` | The validator set at `foreign_validator_height` — a **different** set from the same chain, present only so a planted mutation can substitute a real wrong set instead of a hand-edited one |
| `membership_proof.json` | An ICS-23 existence proof for the `bank` supply key of the network's staking denomination, read at `query_height` |
| `nonmembership_proof.json` | An ICS-23 non-existence proof for a denomination that cannot exist |

Each proof file carries the **ordered pair** of operations the ABCI query
returned: `ics23:iavl` proving the key inside the module's own tree, then
`ics23:simple` proving that tree's root inside the multistore whose root is the
header's `AppHash`. The order is load-bearing.

## Provenance and licence

This data is consensus output produced by the validators of the public
`osmo-test-5` test network and retrieved from a public RPC endpoint. It is
reproduced here verbatim, unmodified, so that the verification in this
repository can be replayed offline by anyone. The MIT licence at the repository
root covers the Go source and does not purport to cover this data.
