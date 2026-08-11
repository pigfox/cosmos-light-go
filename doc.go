// Package cosmoslight replays Cosmos consensus and state proofs offline.
//
// It answers two questions about data captured from a public Cosmos network,
// and it answers them from the captured bytes alone:
//
//   - Did more than two thirds of the validator set's VOTING POWER sign this
//     header? Not two thirds of the validators — voting power is not uniformly
//     distributed and the two thresholds are different numbers. See
//     [VerifyCommit].
//   - Does this key hold this value in the application state that header
//     commits to? That is two ICS-23 proofs chained, not one: the key sits in
//     one module's IAVL tree, and that tree's root sits in the multistore whose
//     root is the header's AppHash. See [VerifyMembership].
//
// [VerifyNonMembership] answers the third question — that a key is ABSENT from
// that same committed state.
//
// # What this is not
//
// This is not a light client. A light client follows a chain: it holds a
// trusted header, requests new ones, checks them against a trust period and a
// trust level, and updates what it trusts. Nothing here does any of that. There
// is no trust period, no bisection, no header store, and no network code of any
// kind at verification time — the only code in this repository that opens a
// socket is the capture test, which is skipped unless an environment variable
// asks for it.
//
// What this does is take one signed header, one validator set and two state
// proofs that were produced by real validators at a real height, and check that
// the cryptography still holds. The fixtures under testdata are frozen at the
// height named in [CaptureMeta]. They do not advance and are not meant to.
//
// That is a deliberate design property rather than a shortcoming. A verifier
// whose inputs are fixed is a verifier whose failures are reproducible: the
// planted-mutation matrix in this package's tests can flip one bit in one
// signature and assert the exact rejection, which is not a thing you can do
// against a live endpoint.
package cosmoslight
