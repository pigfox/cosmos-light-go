package cosmoslight

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cometbft/cometbft/types"
	ics23 "github.com/cosmos/ics23/go"
)

// A VERIFIER THAT CANNOT GO RED IS NOT A VERIFIER.
//
// Every plant below mutates an IN-MEMORY copy of a real captured fixture. The
// files under testdata are never written to, so there is nothing to undo and no
// possibility of an incomplete undo leaving later plants meaningless. Each plant
// reloads from the embedded copy, which is the strongest form of that guarantee
// available: not "reverted correctly" but "never mutated".
//
// The expected outcome is a SPECIFIC sentinel, not merely "an error". A
// mutation that fails for the wrong reason is a plant that has stopped testing
// what it claims to test — the commonest way a matrix like this rots.

// plant is one mutation and the verdict it must produce.
type plant struct {
	id   string
	what string

	// run applies the mutation to a fresh fixture and returns the verifier's
	// verdict. A nil error means the verifier accepted.
	run func(t *testing.T, f *Fixture) error

	// wantErr is the sentinel the verdict must match, or nil if the plant is
	// expected to be ACCEPTED. Not every plant is a rejection: P3 exists to
	// prove the verifier still says yes when it should, so that the others are
	// not passing merely because it says no to everything.
	wantErr error
}

func plants() []plant {
	return []plant{
		{
			id:   "P1",
			what: "flip one bit in one validator signature",
			run: func(_ *testing.T, f *Fixture) error {
				idx := firstCommittingSlot(f)
				f.SignedHeader.Commit.Signatures[idx].Signature[0] ^= 0x01
				_, err := f.VerifyCommit()
				return err
			},
			wantErr: ErrInvalidSignature,
		},
		{
			id:   "P2",
			what: "signed power exactly equal to the threshold",
			// Handled by TestPlantP2ThresholdBoundary, which proves the
			// captured set admits no such subset and then drives the predicate
			// directly. Kept in the table so the matrix prints a row for it.
			run:     nil,
			wantErr: ErrInsufficientVotingPower,
		},
		{
			id:   "P3",
			what: "the minimal validator subset whose power exceeds the threshold",
			run: func(t *testing.T, f *Fixture) error {
				keep := minimalExceedingSubset(f)
				silenceAllBut(f, keep)
				res, err := f.VerifyCommit()
				t.Logf("    P3 kept %d of %d validators, %d of %d power (threshold %d)",
					len(keep), f.Validators.Size(), res.SignedVotingPower, res.TotalVotingPower,
					res.VotingPowerNeeded)
				return err
			},
			wantErr: nil,
		},
		{
			id:   "P3b",
			what: "a MAJORITY of validators holding a minority of power",
			run: func(t *testing.T, f *Fixture) error {
				// The complement of the minimal exceeding subset: on this
				// fixture that is three of five validators — a clear majority
				// by head count, and nowhere near the threshold by power.
				exceed := minimalExceedingSubset(f)
				keep := map[int]bool{}
				for i := range f.Validators.Validators {
					if !exceed[i] {
						keep[i] = true
					}
				}
				silenceAllBut(f, keep)
				res, err := f.VerifyCommit()
				t.Logf("    P3b kept %d of %d validators, %d of %d power (threshold %d)",
					len(keep), f.Validators.Size(), res.SignedVotingPower, res.TotalVotingPower,
					res.VotingPowerNeeded)
				return err
			},
			wantErr: ErrInsufficientVotingPower,
		},
		{
			id:   "P4",
			what: "flip one byte in the value the ICS-23 leaf proof commits to",
			run: func(_ *testing.T, f *Fixture) error {
				f.Membership.Value[0] ^= 0x01
				return f.VerifyMembership()
			},
			wantErr: ErrProofRejected,
		},
		{
			id:   "P5",
			what: "flip one byte in the AppHash",
			run: func(_ *testing.T, f *Fixture) error {
				appHash := append([]byte(nil), f.AppHash()...)
				appHash[0] ^= 0x01
				return VerifyMembership(appHash, f.Membership)
			},
			wantErr: ErrProofRejected,
		},
		{
			id:   "P6",
			what: "verify a valid proof against the wrong ICS-23 ProofSpec",
			run: func(_ *testing.T, f *Fixture) error {
				// The two specs, swapped. Nothing else is touched: the proof,
				// the key, the value and the root are all the genuine ones.
				return verifyBundle(f.AppHash(), f.Membership,
					correctSpecs.root, correctSpecs.leaf, false)
			},
			wantErr: ErrProofRejected,
		},
		{
			id:   "P7",
			what: "substitute the validator set from a different height",
			run: func(t *testing.T, f *Fixture) error {
				t.Logf("    P7 substituted the set at height %d (%d validators) for the one at %d (%d)",
					f.Meta.ForeignValidatorHeight, f.ForeignValidators.Size(),
					f.Meta.HeaderHeight, f.Validators.Size())
				_, err := VerifyCommit(f.Meta.ChainID, f.ForeignValidators, f.SignedHeader)
				return err
			},
			wantErr: ErrValidatorSetMismatch,
		},
	}
}

func TestPlantMatrix(t *testing.T) {
	type row struct {
		id, what, want, got, verdict string
	}
	var rows []row

	for _, p := range plants() {
		p := p
		t.Run(p.id, func(t *testing.T) {
			want := "ACCEPT"
			if p.wantErr != nil {
				want = p.wantErr.Error()
			}

			if p.run == nil {
				rows = append(rows, row{p.id, p.what, want, "see TestPlantP2ThresholdBoundary", "PASS"})
				t.Skip("driven by TestPlantP2ThresholdBoundary; see its comment for why")
			}

			err := p.run(t, load(t))

			got := "ACCEPT"
			if err != nil {
				got = err.Error()
			}

			verdict := "PASS"
			switch {
			case p.wantErr == nil && err != nil:
				verdict = "FAIL"
				t.Errorf("expected the verifier to ACCEPT, got: %v", err)
			case p.wantErr != nil && err == nil:
				// The dangerous direction, and the one worth spelling out: a
				// mutation that sails through means the premise of the plant is
				// wrong, or the check it targets is not running at all.
				verdict = "FAIL-GREEN"
				t.Errorf("the mutation was ACCEPTED. Expected %v. A plant that comes back green "+
					"means either the verifier is not performing this check, or the mutation "+
					"does not do what its description says.", p.wantErr)
			case p.wantErr != nil && !errors.Is(err, p.wantErr):
				verdict = "FAIL-WRONG-REASON"
				t.Errorf("rejected for the wrong reason: got %v, want %v", err, p.wantErr)
			}

			rows = append(rows, row{p.id, p.what, want, got, verdict})
		})
	}

	t.Log("planted-failure matrix")
	for _, r := range rows {
		t.Log(fmt.Sprintf("  %-4s %-6s %-58s want=%s", r.id, r.verdict, r.what, r.want))
		if r.verdict != "PASS" {
			t.Log(fmt.Sprintf("       got=%s", r.got))
		}
	}
}

// TestPlantP2ThresholdBoundary is P2, and it is a separate test because the
// plant as originally specified CANNOT BE BUILT from this fixture.
//
// P2 asks for a signing subset whose power is EXACTLY the threshold, to drive
// the `>` versus `>=` boundary. The threshold here is 57,130,044, and the
// captured set's five voting powers admit no subset summing to it — that is
// proved below by enumerating every subset rather than asserted. It is not a
// coincidence to be engineered around either: `needed` is
// `total * 2 / 3` truncated, and there is no reason a real set's powers should
// happen to hit it.
//
// So the boundary is driven at the predicate that decides it. That is a weaker
// plant than a byte-level mutation and it is recorded as weaker rather than
// dressed up: what it proves is that the comparison is strict, not that a
// crafted commit at exactly the threshold would be refused end to end.
func TestPlantP2ThresholdBoundary(t *testing.T) {
	f := load(t)

	powers := make([]int64, 0, f.Validators.Size())
	for _, v := range f.Validators.Validators {
		powers = append(powers, v.VotingPower)
	}
	total := f.Validators.TotalVotingPower()
	needed := total * commitPowerNumerator / commitPowerDenominator

	if len(powers) > 20 {
		t.Fatalf("the captured set has %d validators; enumerating every subset is no longer "+
			"cheap and this proof needs rewriting", len(powers))
	}

	var exact [][]int64
	for mask := 0; mask < 1<<len(powers); mask++ {
		var sum int64
		var members []int64
		for i, p := range powers {
			if mask&(1<<i) != 0 {
				sum += p
				members = append(members, p)
			}
		}
		if sum == needed {
			exact = append(exact, members)
		}
	}

	if len(exact) > 0 {
		// If a future capture DOES admit an exact subset, this plant can and
		// must be built properly. Failing here is the prompt to do that.
		t.Fatalf("this fixture admits %d subset(s) summing to exactly the threshold %d, e.g. %v. "+
			"P2 can now be built as a real signature-level mutation and should be.",
			len(exact), needed, exact[0])
	}
	t.Logf("no subset of %v sums to exactly %d (all %d subsets enumerated); the boundary is "+
		"driven at the predicate instead", powers, needed, 1<<len(powers))

	if powerSuffices(needed, needed) {
		t.Error("exactly the threshold was ACCEPTED. CometBFT rejects `tallied <= needed`; a " +
			"verifier that accepts precisely two thirds of the voting power is permissive in " +
			"the one place it must not be.")
	}
	if !powerSuffices(needed+1, needed) {
		t.Error("one unit above the threshold was rejected, so the comparison is too strict")
	}
}

// --- plant helpers ---------------------------------------------------------

// firstCommittingSlot returns the index of the first signature slot that
// actually commits to the block. Mutating an absent slot would prove nothing,
// because an absent slot is never checked.
func firstCommittingSlot(f *Fixture) int {
	for i, s := range f.SignedHeader.Commit.Signatures {
		if s.BlockIDFlag == types.BlockIDFlagCommit {
			return i
		}
	}
	return -1
}

// minimalExceedingSubset returns the fewest validator indices, taken in
// descending power order, whose combined power exceeds the threshold.
func minimalExceedingSubset(f *Fixture) map[int]bool {
	needed := f.Validators.TotalVotingPower() * commitPowerNumerator / commitPowerDenominator

	type indexed struct {
		idx   int
		power int64
	}
	order := make([]indexed, 0, f.Validators.Size())
	for i, v := range f.Validators.Validators {
		order = append(order, indexed{i, v.VotingPower})
	}
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && order[j].power > order[j-1].power; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}

	keep := map[int]bool{}
	var acc int64
	for _, o := range order {
		keep[o.idx] = true
		acc += o.power
		if powerSuffices(acc, needed) {
			break
		}
	}
	return keep
}

// silenceAllBut marks every signature slot outside keep as ABSENT.
//
// Absent is the honest way to remove a signer: it is what a real commit records
// for a validator that did not sign, so the mutated commit stays a
// well-formed commit and the verifier's rejection is about voting power rather
// than about a malformed structure.
func silenceAllBut(f *Fixture, keep map[int]bool) {
	for i := range f.SignedHeader.Commit.Signatures {
		if keep[i] {
			continue
		}
		f.SignedHeader.Commit.Signatures[i] = types.CommitSig{BlockIDFlag: types.BlockIDFlagAbsent}
	}
}

// compile-time reminder that the plant matrix uses the real spec objects.
var _ = []*ics23.ProofSpec{correctSpecs.leaf, correctSpecs.root}
