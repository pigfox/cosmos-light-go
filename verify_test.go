package cosmoslight

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cometbft/cometbft/types"
	ics23 "github.com/cosmos/ics23/go"
)

// load returns a fresh fixture. Every test and every plant takes its own copy,
// so no test can be affected by another's mutations.
func load(t *testing.T) *Fixture {
	t.Helper()
	f, err := Load()
	if err != nil {
		t.Fatalf("load the embedded fixture: %v", err)
	}
	return f
}

func TestFixtureLoads(t *testing.T) {
	f := load(t)

	if f.Meta.ChainID == "" || f.Meta.Endpoint == "" || f.Meta.CapturedAtUTC == "" {
		t.Errorf("the capture record is incomplete: %+v", f.Meta)
	}
	// The off-by-one that decides whether the proofs verify at all.
	if f.Meta.HeaderHeight != f.Meta.QueryHeight+1 {
		t.Errorf("header height %d is not one above query height %d; the AppHash in a header "+
			"roots the state the PREVIOUS block left behind",
			f.Meta.HeaderHeight, f.Meta.QueryHeight)
	}
	if f.SignedHeader.Header.Height != f.Meta.HeaderHeight {
		t.Errorf("the captured header is at %d, the record says %d",
			f.SignedHeader.Header.Height, f.Meta.HeaderHeight)
	}
	if f.SignedHeader.Header.ChainID != f.Meta.ChainID {
		t.Errorf("the captured header is for %q, the record says %q",
			f.SignedHeader.Header.ChainID, f.Meta.ChainID)
	}
	if f.Membership.QueryHeight != f.Meta.QueryHeight {
		t.Errorf("the existence proof was read at %d, the record says %d",
			f.Membership.QueryHeight, f.Meta.QueryHeight)
	}
	// A non-existence bundle with a value would not be a non-existence bundle.
	if f.NonMembership.Value != nil {
		t.Errorf("the absence bundle carries a value: %q", f.NonMembership.Value)
	}
}

func TestVerifyCommit(t *testing.T) {
	f := load(t)

	res, err := f.VerifyCommit()
	if err != nil {
		t.Fatalf("the captured commit does not verify: %v", err)
	}

	if res.ChainID != f.Meta.ChainID || res.Height != f.Meta.HeaderHeight {
		t.Errorf("result identifies %s@%d, fixture is %s@%d",
			res.ChainID, res.Height, f.Meta.ChainID, f.Meta.HeaderHeight)
	}
	if res.ValidatorCount == 0 || res.TotalVotingPower == 0 {
		t.Fatal("the result reports an empty set, so its verdict means nothing")
	}
	if res.VotingPowerNeeded != res.TotalVotingPower*commitPowerNumerator/commitPowerDenominator {
		t.Errorf("threshold %d is not %d/%d of %d",
			res.VotingPowerNeeded, commitPowerNumerator, commitPowerDenominator, res.TotalVotingPower)
	}
	if !powerSuffices(res.SignedVotingPower, res.VotingPowerNeeded) {
		t.Errorf("accepted with %d of %d power, threshold %d",
			res.SignedVotingPower, res.TotalVotingPower, res.VotingPowerNeeded)
	}

	t.Logf("%s height %d: %d/%d validators, %d/%d voting power, threshold %d",
		res.ChainID, res.Height, res.SignedCount, res.ValidatorCount,
		res.SignedVotingPower, res.TotalVotingPower, res.VotingPowerNeeded)
}

// THE CLAIM THE PACKAGE DOC MAKES, ASSERTED. If the captured set had uniform
// power, "two thirds of power" and "two thirds of validators" would coincide,
// every plant below would still pass, and the distinction this package exists
// to make would be untestable. That would not be a failure of the code — but it
// would mean the fixture no longer demonstrates anything, and a later
// re-capture could silently land there.
func TestCapturedSetHasUnequalPower(t *testing.T) {
	f := load(t)

	powers := map[int64]int{}
	for _, v := range f.Validators.Validators {
		powers[v.VotingPower]++
	}
	if len(powers) < 2 {
		t.Fatalf("every validator in the captured set holds the same power, so voting power "+
			"and validator count cannot be told apart; re-capture from a chain with an "+
			"uneven set. Powers seen: %v", powers)
	}

	// Stronger, and the property the README quotes: some strict MINORITY of
	// validators must hold more than the threshold, so that counting heads and
	// counting power genuinely disagree.
	total := f.Validators.TotalVotingPower()
	needed := total * commitPowerNumerator / commitPowerDenominator
	sorted := sortedByPowerDesc(f.Validators)

	var acc int64
	var n int
	for _, v := range sorted {
		acc += v.VotingPower
		n++
		if powerSuffices(acc, needed) {
			break
		}
	}
	if n*2 >= len(sorted) {
		t.Errorf("it takes %d of %d validators to exceed the threshold, which is not a "+
			"minority; the count/power distinction is not demonstrated by this fixture",
			n, len(sorted))
	}
	t.Logf("%d of %d validators (%d of %d power) exceed the threshold",
		n, len(sorted), acc, total)
}

func TestVerifyMembership(t *testing.T) {
	f := load(t)
	if err := f.VerifyMembership(); err != nil {
		t.Fatalf("the captured existence proof does not verify: %v", err)
	}
	t.Logf("store %q key %X holds %q at height %d",
		f.Membership.Store, f.Membership.Key, f.Membership.Value, f.Membership.QueryHeight)
}

func TestVerifyNonMembership(t *testing.T) {
	f := load(t)
	if err := f.VerifyNonMembership(); err != nil {
		t.Fatalf("the captured absence proof does not verify: %v", err)
	}
	t.Logf("store %q key %X is absent at height %d",
		f.NonMembership.Store, f.NonMembership.Key, f.NonMembership.QueryHeight)
}

// powerSuffices is driven directly at the boundary, in both directions. This is
// the only way to reach `tallied == needed` exactly: see plant P2, where the
// captured validator set is proved to admit no subset summing to that value.
func TestPowerSufficesBoundary(t *testing.T) {
	const needed = 57130044

	if powerSuffices(needed, needed) {
		t.Error("exactly the threshold was accepted; CometBFT rejects `tallied <= needed`, so " +
			"a commit carrying precisely two thirds of the power must NOT pass")
	}
	if !powerSuffices(needed+1, needed) {
		t.Error("one unit above the threshold was rejected")
	}
	if powerSuffices(needed-1, needed) {
		t.Error("one unit below the threshold was accepted")
	}
}

// --- error paths -----------------------------------------------------------

func TestVerifyCommitRejectsMalformedInput(t *testing.T) {
	f := load(t)

	for _, tc := range []struct {
		name string
		vals *types.ValidatorSet
		sh   *types.SignedHeader
	}{
		{"nil validators", nil, f.SignedHeader},
		{"nil header", f.Validators, nil},
		{"nil inner header", f.Validators, &types.SignedHeader{Commit: f.SignedHeader.Commit}},
		{"nil commit", f.Validators, &types.SignedHeader{Header: f.SignedHeader.Header}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := VerifyCommit(f.Meta.ChainID, tc.vals, tc.sh); !errors.Is(err, ErrMalformedFixture) {
				t.Errorf("got %v, want ErrMalformedFixture", err)
			}
		})
	}
}

func TestVerifyCommitRejectsWrongChainID(t *testing.T) {
	f := load(t)
	_, err := VerifyCommit(f.Meta.ChainID+"-not", f.Validators, f.SignedHeader)
	if !errors.Is(err, ErrChainIDMismatch) {
		t.Errorf("got %v, want ErrChainIDMismatch", err)
	}
}

func TestVerifyCommitRejectsSignatureCountMismatch(t *testing.T) {
	f := load(t)

	// Drop a signature SLOT without touching the validator set. The set still
	// hashes correctly, so this reaches the positional-correspondence check
	// rather than the set check ahead of it.
	f.SignedHeader.Commit.Signatures = f.SignedHeader.Commit.Signatures[:len(f.SignedHeader.Commit.Signatures)-1]

	_, err := f.VerifyCommit()
	if !errors.Is(err, ErrSignatureCountMismatch) {
		t.Errorf("got %v, want ErrSignatureCountMismatch", err)
	}
}

func TestVerifyBundleRejectsMalformedShapes(t *testing.T) {
	f := load(t)
	appHash := f.AppHash()

	t.Run("nil bundle", func(t *testing.T) {
		if err := VerifyMembership(appHash, nil); !errors.Is(err, ErrMalformedFixture) {
			t.Errorf("got %v, want ErrMalformedFixture", err)
		}
	})

	t.Run("wrong operation count", func(t *testing.T) {
		b := load(t).Membership
		b.Ops = b.Ops[:1]
		if err := VerifyMembership(appHash, b); !errors.Is(err, ErrProofShape) {
			t.Errorf("got %v, want ErrProofShape", err)
		}
	})

	t.Run("operations out of order", func(t *testing.T) {
		b := load(t).Membership
		b.Ops[proofOpIndexIAVL], b.Ops[proofOpIndexSimple] = b.Ops[proofOpIndexSimple], b.Ops[proofOpIndexIAVL]
		if err := VerifyMembership(appHash, b); !errors.Is(err, ErrProofShape) {
			t.Errorf("got %v, want ErrProofShape", err)
		}
	})

	t.Run("leaf operation is for another key", func(t *testing.T) {
		b := load(t).Membership
		b.Ops[proofOpIndexIAVL].Key = append([]byte{}, append(b.Key, 0x99)...)
		if err := VerifyMembership(appHash, b); !errors.Is(err, ErrProofShape) {
			t.Errorf("got %v, want ErrProofShape", err)
		}
	})

	t.Run("root operation names another store", func(t *testing.T) {
		b := load(t).Membership
		b.Ops[proofOpIndexSimple].Key = []byte("not-" + b.Store)
		if err := VerifyMembership(appHash, b); !errors.Is(err, ErrProofShape) {
			t.Errorf("got %v, want ErrProofShape", err)
		}
	})

	t.Run("undecodable proof bytes", func(t *testing.T) {
		b := load(t).Membership
		b.Ops[proofOpIndexSimple].Data = []byte{0xff, 0xff, 0xff, 0xff}
		if err := VerifyMembership(appHash, b); !errors.Is(err, ErrMalformedFixture) {
			t.Errorf("got %v, want ErrMalformedFixture", err)
		}
	})

	t.Run("undecodable leaf proof bytes", func(t *testing.T) {
		b := load(t).Membership
		b.Ops[proofOpIndexIAVL].Data = []byte{0xff, 0xff, 0xff, 0xff}
		if err := VerifyMembership(appHash, b); !errors.Is(err, ErrMalformedFixture) {
			t.Errorf("got %v, want ErrMalformedFixture", err)
		}
	})

	t.Run("multistore operation is not an existence proof", func(t *testing.T) {
		// The absence bundle's LEAF operation is a non-existence proof. Putting
		// it where the multistore operation belongs reaches the check that the
		// multistore layer must always be an existence proof.
		b := load(t).Membership
		nonExist := load(t).NonMembership
		b.Ops[proofOpIndexSimple].Data = nonExist.Ops[proofOpIndexIAVL].Data
		if err := VerifyMembership(appHash, b); !errors.Is(err, ErrProofShape) {
			t.Errorf("got %v, want ErrProofShape", err)
		}
	})

	t.Run("absence bundle carrying a value", func(t *testing.T) {
		b := load(t).NonMembership
		b.Value = []byte("something")
		if err := VerifyNonMembership(appHash, b); !errors.Is(err, ErrMalformedFixture) {
			t.Errorf("got %v, want ErrMalformedFixture", err)
		}
	})

	// The absence proof must also be REFUSABLE, and getting there taught
	// something worth writing down.
	//
	// The first attempt flipped one bit of the absent key and expected a
	// rejection. It was ACCEPTED, and correctly so: an ICS-23 non-existence
	// proof pins the two existing neighbours that BRACKET the queried key, so
	// it proves the absence of every key in that gap, not just the one asked
	// about. A near neighbour of an absent key is usually still in the gap. The
	// verifier was right and the premise was wrong.
	//
	// So this asks the absence proof about a key that demonstrably EXISTS — the
	// one the membership fixture proves the value of. No gap can contain it.
	//
	// Both the bundle's key and the leaf operation's key move together, because
	// moving only one trips the shape check first and would never reach the
	// ICS-23 call this case exists to exercise.
	t.Run("absence proof asked about a key that exists", func(t *testing.T) {
		b := load(t).NonMembership
		present := load(t).Membership.Key
		b.Key = present
		b.Ops[proofOpIndexIAVL].Key = present
		if err := VerifyNonMembership(appHash, b); !errors.Is(err, ErrProofRejected) {
			t.Errorf("got %v, want ErrProofRejected", err)
		}
	})
}

// TestLoadFSReportsWhicheverFileIsMissing walks every fixture file, removes it
// from a complete copy, and requires the load to fail.
//
// It is a walk rather than one hand-picked case because the loader reads six
// files and an untested read is a read whose error branch might be `return nil`.
func TestLoadFSReportsWhicheverFileIsMissing(t *testing.T) {
	files := []string{
		fileCaptureMeta, fileSignedHeader, fileValidators,
		fileForeignValidators, fileMembership, fileNonMembership,
	}
	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			dir := writeFixtureCopy(t)
			if err := os.Remove(filepath.Join(dir, testdataDir, name)); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadFS(os.DirFS(dir)); !errors.Is(err, ErrMalformedFixture) {
				t.Errorf("removing %s did not fail the load: %v", name, err)
			}
		})
	}
}

// TestLoadFSRejectsValidatorsThatAreNotAnArray reaches the codec's own error,
// which is a different branch from a file that will not parse as JSON at all.
func TestLoadFSRejectsValidatorsThatAreNotAnArray(t *testing.T) {
	for _, name := range []string{fileValidators, fileForeignValidators} {
		t.Run(name, func(t *testing.T) {
			dir := writeFixtureCopy(t)
			if err := os.WriteFile(filepath.Join(dir, testdataDir, name), []byte(`{"not":"an array"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadFS(os.DirFS(dir)); !errors.Is(err, ErrMalformedFixture) {
				t.Errorf("got %v, want ErrMalformedFixture", err)
			}
		})
	}
}

func TestLoadFSRejectsMissingAndBrokenFiles(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		if _, err := LoadFS(os.DirFS(t.TempDir())); !errors.Is(err, ErrMalformedFixture) {
			t.Errorf("got %v, want ErrMalformedFixture", err)
		}
	})

	t.Run("unparseable", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, testdataDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, testdataDir, fileCaptureMeta), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFS(os.DirFS(dir)); !errors.Is(err, ErrMalformedFixture) {
			t.Errorf("got %v, want ErrMalformedFixture", err)
		}
	})

	t.Run("empty validator array", func(t *testing.T) {
		dir := writeFixtureCopy(t)
		if err := os.WriteFile(filepath.Join(dir, testdataDir, fileValidators), []byte("[]"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFS(os.DirFS(dir)); !errors.Is(err, ErrMalformedFixture) {
			t.Errorf("got %v, want ErrMalformedFixture", err)
		}
	})

	t.Run("validator that fails validation", func(t *testing.T) {
		dir := writeFixtureCopy(t)
		bad := `[{"address":"0000000000000000000000000000000000000000",` +
			`"pub_key":{"type":"tendermint/PubKeyEd25519","value":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="},` +
			`"voting_power":"-1","proposer_priority":"0"}]`
		if err := os.WriteFile(filepath.Join(dir, testdataDir, fileValidators), []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFS(os.DirFS(dir)); !errors.Is(err, ErrMalformedFixture) {
			t.Errorf("got %v, want ErrMalformedFixture", err)
		}
	})

	t.Run("undecodable signed header", func(t *testing.T) {
		dir := writeFixtureCopy(t)
		if err := os.WriteFile(filepath.Join(dir, testdataDir, fileSignedHeader), []byte(`"not a header"`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFS(os.DirFS(dir)); !errors.Is(err, ErrMalformedFixture) {
			t.Errorf("got %v, want ErrMalformedFixture", err)
		}
	})
}

// writeFixtureCopy materialises the embedded fixture into a temp directory so a
// single file can be corrupted without touching the real one.
func writeFixtureCopy(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, testdataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		fileCaptureMeta, fileSignedHeader, fileValidators,
		fileForeignValidators, fileMembership, fileNonMembership,
	} {
		b, err := embedded.ReadFile(testdataDir + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, testdataDir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// --- the no-network property, as structure rather than prose ----------------

// TestNoNetworkImportsOutsideCapture reads IMPORT DECLARATIONS, not text.
//
// The package doc, the README and several comments in this repository discuss
// the network at length — explaining precisely that verification does not touch
// it. A text scan would be satisfied by that prose long after a real import
// appeared, and would flag the honest comments meanwhile. This is the trap a
// grep-based doctrine gate hits every time; parsing is the fix.
func TestNoNetworkImportsOutsideCapture(t *testing.T) {
	banned := map[string]bool{
		"net":        true,
		"net/http":   true,
		"net/url":    true,
		"os/exec":    true,
		"crypto/tls": true,
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}

	var scanned int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || name == "capture_test.go" {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		for _, imp := range file.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if banned[p] {
				t.Errorf("%s imports %q. Verification is pure computation over bytes on disk; "+
					"the only file allowed to reach the network is capture_test.go, which is "+
					"not compiled into a consumer's binary.", name, p)
			}
		}
	}

	// Refuse a vacuous pass: if the walk found nothing, it proved nothing.
	if scanned == 0 {
		t.Fatal("no Go files were parsed, so this guard asserted nothing")
	}
	// And prove the scan can see imports at all, by finding one that is there.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "capture_test.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse capture_test.go: %v", err)
	}
	var sawHTTP bool
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) == "net/http" {
			sawHTTP = true
		}
	}
	if !sawHTTP {
		t.Error("the scan cannot find net/http in capture_test.go, which plainly imports it; " +
			"it is not reading import declarations and its clean findings mean nothing")
	}
	t.Logf("scanned %d non-capture file(s)", scanned)
}

// --- helpers shared with the plant matrix ----------------------------------

// sortedByPowerDesc returns the validators ordered by descending voting power.
// The set is already in that order; this makes the dependence explicit rather
// than relying on it silently.
func sortedByPowerDesc(vals *types.ValidatorSet) []*types.Validator {
	out := make([]*types.Validator, len(vals.Validators))
	copy(out, vals.Validators)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].VotingPower > out[j-1].VotingPower; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// specPair names an ICS-23 spec combination for the plant matrix.
type specPair struct {
	leaf, root *ics23.ProofSpec
}

var correctSpecs = specPair{leaf: ics23.IavlSpec, root: ics23.TendermintSpec}
