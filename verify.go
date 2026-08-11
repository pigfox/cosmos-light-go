package cosmoslight

import (
	"bytes"
	"fmt"

	"github.com/cometbft/cometbft/types"
	ics23 "github.com/cosmos/ics23/go"
)

// CommitResult is what [VerifyCommit] learned on the way to its verdict.
//
// IT REPORTS COUNT AND POWER SEPARATELY, and that is the point of the type
// existing at all. On a real network these two disagree: a set can be a
// minority of validators holding a majority of power, or the reverse. Returning
// only a boolean would let a caller render "97 of 100 validators signed" beside
// a verdict that was actually decided by SignedVotingPower, and a reader would
// have no way to notice the substitution.
type CommitResult struct {
	ChainID string
	Height  int64

	// ValidatorCount is every validator in the set; SignedCount is how many of
	// them produced a signature that verified and counted toward the block.
	ValidatorCount int
	SignedCount    int

	// TotalVotingPower is the set's whole power. SignedVotingPower is the part
	// of it that verified. VotingPowerNeeded is the integer threshold the tally
	// must strictly EXCEED — see [commitPowerNumerator].
	TotalVotingPower  int64
	SignedVotingPower int64
	VotingPowerNeeded int64
}

// VerifyCommit checks that more than two thirds of the validator set's voting
// power signed the header.
//
// The steps are ordered so that a cheap structural mismatch is reported as
// itself rather than as a signature failure: chain ID, then the validator-set
// hash the header commits to, then the positional correspondence between
// signature slots and validators, and only then the signatures.
func VerifyCommit(chainID string, vals *types.ValidatorSet, sh *types.SignedHeader) (CommitResult, error) {
	var res CommitResult

	if vals == nil || sh == nil || sh.Header == nil || sh.Commit == nil {
		return res, fmt.Errorf("%w: nil header, commit or validator set", ErrMalformedFixture)
	}

	res.ChainID = sh.Header.ChainID
	res.Height = sh.Header.Height
	res.ValidatorCount = vals.Size()
	res.TotalVotingPower = vals.TotalVotingPower()
	res.VotingPowerNeeded = res.TotalVotingPower * commitPowerNumerator / commitPowerDenominator

	if sh.Header.ChainID != chainID {
		return res, fmt.Errorf("%w: header says %q, verifying against %q",
			ErrChainIDMismatch, sh.Header.ChainID, chainID)
	}

	// THE SET MUST BE THE ONE THIS HEADER COMMITS TO. Without this, a caller
	// could hand over any set at all and the signature loop would simply find
	// nothing that verifies — reported as a signature failure, which is a
	// misleading diagnosis of a substituted set.
	if !bytes.Equal(vals.Hash(), sh.Header.ValidatorsHash) {
		return res, fmt.Errorf("%w: set hashes to %X, header commits to %X",
			ErrValidatorSetMismatch, vals.Hash(), sh.Header.ValidatorsHash)
	}

	// CometBFT commits are POSITIONAL: slot i belongs to validator i. A pair of
	// differing lengths cannot be read that way at all.
	if len(sh.Commit.Signatures) != vals.Size() {
		return res, fmt.Errorf("%w: %d signature slots, %d validators",
			ErrSignatureCountMismatch, len(sh.Commit.Signatures), vals.Size())
	}

	for idx, commitSig := range sh.Commit.Signatures {
		// A slot that did not commit to THIS block contributes nothing. Absent
		// and nil-vote slots are both normal and are not failures.
		if commitSig.BlockIDFlag != types.BlockIDFlagCommit {
			continue
		}

		val := vals.Validators[idx]
		signBytes := sh.Commit.VoteSignBytes(chainID, int32(idx))
		if !val.PubKey.VerifySignature(signBytes, commitSig.Signature) {
			return res, fmt.Errorf("%w: slot %d, validator %X",
				ErrInvalidSignature, idx, val.Address)
		}

		res.SignedCount++
		res.SignedVotingPower += val.VotingPower
	}

	if !powerSuffices(res.SignedVotingPower, res.VotingPowerNeeded) {
		return res, fmt.Errorf("%w: %d of %d power signed, need more than %d",
			ErrInsufficientVotingPower, res.SignedVotingPower, res.TotalVotingPower, res.VotingPowerNeeded)
	}

	return res, nil
}

// powerSuffices is the threshold decision, alone, so the boundary can be driven
// directly at `needed` and `needed+1` rather than only through whatever subset
// sums a particular captured validator set happens to allow.
//
// STRICTLY GREATER. Exactly the threshold is not enough. This is CometBFT's own
// rule (`got <= needed` rejects, in types/validation.go) and getting it wrong
// yields a verifier that is subtly permissive in the one place it must not be.
func powerSuffices(tallied, needed int64) bool {
	return tallied > needed
}

// ProofOp is one operation of a Cosmos store proof, as the ABCI query returned
// it. Data is the protobuf-encoded ICS-23 CommitmentProof.
type ProofOp struct {
	Type string `json:"type"`
	Key  []byte `json:"key"`
	Data []byte `json:"data"`
}

// ProofBundle is one captured state proof: the key that was queried, the value
// the node returned for it, and the operation pair that proves it.
//
// Value is nil for a non-existence proof, which is a real distinction rather
// than a missing field — a key proven ABSENT has no value by definition.
type ProofBundle struct {
	Store       string    `json:"store"`
	QueryHeight int64     `json:"query_height"`
	Key         []byte    `json:"key"`
	Value       []byte    `json:"value"`
	Ops         []ProofOp `json:"ops"`
}

// VerifyMembership checks that the bundle's key holds its value in the
// application state the given AppHash commits to.
//
// TWO PROOFS, CHAINED. The first operation proves the key inside one module's
// IAVL tree and yields that tree's root; the second proves that root inside the
// multistore, whose own root is the AppHash. Checking only one of them proves
// only half the path, and the half it proves is the half an untrusted node
// could have made up.
func VerifyMembership(appHash []byte, b *ProofBundle) error {
	return verifyBundle(appHash, b, ics23.IavlSpec, ics23.TendermintSpec, false)
}

// VerifyNonMembership checks that the bundle's key is ABSENT from the
// application state the given AppHash commits to.
func VerifyNonMembership(appHash []byte, b *ProofBundle) error {
	return verifyBundle(appHash, b, ics23.IavlSpec, ics23.TendermintSpec, true)
}

// verifyBundle carries both specs as parameters rather than reading the
// package-level ones directly, so plant P6 can hand it a mismatched pair and
// assert the rejection. Production callers never choose: the two exported
// wrappers above pass the only correct combination.
func verifyBundle(appHash []byte, b *ProofBundle, leafSpec, rootSpec *ics23.ProofSpec, absent bool) error {
	if b == nil {
		return fmt.Errorf("%w: nil proof bundle", ErrMalformedFixture)
	}
	if len(b.Ops) != proofOpCount {
		return fmt.Errorf("%w: %d operations, expected %d", ErrProofShape, len(b.Ops), proofOpCount)
	}

	iavlOp, simpleOp := b.Ops[proofOpIndexIAVL], b.Ops[proofOpIndexSimple]
	if iavlOp.Type != proofOpTypeIAVL || simpleOp.Type != proofOpTypeSimple {
		return fmt.Errorf("%w: operations are %q then %q, expected %q then %q",
			ErrProofShape, iavlOp.Type, simpleOp.Type, proofOpTypeIAVL, proofOpTypeSimple)
	}
	// The operations carry the key each one is about. If those disagree with
	// the bundle, the proof is for a different question than the one asked.
	if !bytes.Equal(iavlOp.Key, b.Key) {
		return fmt.Errorf("%w: leaf operation is for key %X, bundle asks about %X",
			ErrProofShape, iavlOp.Key, b.Key)
	}
	if string(simpleOp.Key) != b.Store {
		return fmt.Errorf("%w: root operation is for store %q, bundle names %q",
			ErrProofShape, simpleOp.Key, b.Store)
	}

	simpleProof, err := decodeProof(simpleOp.Data)
	if err != nil {
		return err
	}
	exist := simpleProof.GetExist()
	if exist == nil {
		return fmt.Errorf("%w: the multistore operation is not an existence proof", ErrProofShape)
	}
	// The value the multistore commits to for this store IS that store's root.
	storeRoot := exist.Value

	if !ics23.VerifyMembership(rootSpec, appHash, simpleProof, []byte(b.Store), storeRoot) {
		return fmt.Errorf("%w: store %q does not commit to root %X under app hash %X",
			ErrProofRejected, b.Store, storeRoot, appHash)
	}

	leafProof, err := decodeProof(iavlOp.Data)
	if err != nil {
		return err
	}

	if absent {
		if b.Value != nil {
			return fmt.Errorf("%w: a non-existence bundle carries a value", ErrMalformedFixture)
		}
		if !ics23.VerifyNonMembership(leafSpec, storeRoot, leafProof, b.Key) {
			return fmt.Errorf("%w: key %X is not proven absent from store %q",
				ErrProofRejected, b.Key, b.Store)
		}
		return nil
	}

	if !ics23.VerifyMembership(leafSpec, storeRoot, leafProof, b.Key, b.Value) {
		return fmt.Errorf("%w: key %X does not hold the given value in store %q",
			ErrProofRejected, b.Key, b.Store)
	}
	return nil
}

// decodeProof unmarshals a protobuf CommitmentProof.
func decodeProof(data []byte) (*ics23.CommitmentProof, error) {
	var p ics23.CommitmentProof
	if err := p.Unmarshal(data); err != nil {
		return nil, fmt.Errorf("%w: proof does not decode: %w", ErrMalformedFixture, err)
	}
	return &p, nil
}
