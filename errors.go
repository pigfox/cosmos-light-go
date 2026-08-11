package cosmoslight

import "errors"

// The sentinels. Callers match with [errors.Is]; every wrapping site uses %w so
// that stays true down the chain. Nothing in this package compares errors with
// ==, and nothing returns a bare string a caller would have to match on.
var (
	// ErrMalformedFixture means the captured bytes could not be read as what
	// they claim to be. It is deliberately separate from the verification
	// failures below: a fixture that will not parse proves nothing either way,
	// and reporting it as "verification failed" would be a false negative.
	ErrMalformedFixture = errors.New("cosmoslight: malformed fixture")

	// ErrChainIDMismatch means the header was signed for a different network
	// than the one being verified against. Vote sign bytes commit to the chain
	// ID, so this is a real defense and not a label check.
	ErrChainIDMismatch = errors.New("cosmoslight: chain ID mismatch")

	// ErrValidatorSetMismatch means the validator set does not hash to the
	// ValidatorsHash the header commits to — the set is not the one that was
	// in force at this height. Plant P7 is exactly this.
	ErrValidatorSetMismatch = errors.New("cosmoslight: validator set does not match the header")

	// ErrSignatureCountMismatch means the commit carries a different number of
	// signature slots than the set has validators. CometBFT's commits are
	// positional, so an index-aligned read of a mismatched pair would compare
	// the wrong validator against the wrong signature.
	ErrSignatureCountMismatch = errors.New("cosmoslight: commit signature count does not match the validator set")

	// ErrInvalidSignature means a signature slot that claimed to commit to the
	// block did not verify under its validator's key. Plant P1 is this.
	ErrInvalidSignature = errors.New("cosmoslight: invalid validator signature")

	// ErrInsufficientVotingPower means the signatures that did verify carry at
	// most two thirds of the total voting power. Plants P2 and P3 sit on either
	// side of this line.
	ErrInsufficientVotingPower = errors.New("cosmoslight: signed voting power does not exceed the threshold")

	// ErrProofShape means the proof operation pair was not the shape a Cosmos
	// store query produces — wrong count, wrong types, or wrong order.
	ErrProofShape = errors.New("cosmoslight: unexpected proof operation shape")

	// ErrProofRejected means ICS-23 rejected the proof against the root it was
	// checked against. Plants P4, P5 and P6 all land here.
	ErrProofRejected = errors.New("cosmoslight: ICS-23 rejected the proof")
)
