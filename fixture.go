package cosmoslight

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"

	cmtjson "github.com/cometbft/cometbft/libs/json"
	"github.com/cometbft/cometbft/types"
)

// The captured chain data travels WITH the module.
//
// A consumer that had to re-capture would need network access at build time and
// would get a different height than the one this package's tests pin, so the
// two would verify different bytes and only one of them would be the bytes
// anybody reviewed.
//
//go:embed testdata
var embedded embed.FS

// CaptureMeta records where every other file in testdata came from. It is
// written by the capture test and is never edited by hand: a provenance record
// somebody can adjust is a provenance record that will eventually be wrong.
type CaptureMeta struct {
	Endpoint        string `json:"endpoint"`
	ChainID         string `json:"chain_id"`
	CometBFTVersion string `json:"cometbft_version"`
	CapturedAtUTC   string `json:"captured_at_utc"`

	// QueryHeight is the height the state was read at. HeaderHeight is the
	// height whose header commits to that state, and it is QueryHeight+1 — the
	// AppHash in a Cosmos header is the root of the state left behind by the
	// PREVIOUS block. Both are recorded because the off-by-one between them is
	// the single easiest thing to get wrong here, and the capture test asserts
	// the relationship rather than assuming it.
	QueryHeight  int64 `json:"query_height"`
	HeaderHeight int64 `json:"header_height"`

	// ForeignValidatorHeight is where the deliberately-wrong validator set was
	// read from. It is recorded so the plant matrix can say which height it
	// substituted rather than describing it vaguely as "another".
	ForeignValidatorHeight int64 `json:"foreign_validator_height"`

	Store string `json:"store"`
}

// Fixture is one complete capture, decoded.
type Fixture struct {
	Meta         CaptureMeta
	SignedHeader *types.SignedHeader
	Validators   *types.ValidatorSet

	// ForeignValidators governed the same chain at a different height. It is
	// here for the plant matrix and has no production use.
	ForeignValidators *types.ValidatorSet

	Membership    *ProofBundle
	NonMembership *ProofBundle
}

// Load reads the fixture embedded in this module.
func Load() (*Fixture, error) { return LoadFS(embedded) }

// LoadFS reads a fixture from any filesystem laid out like testdata/.
//
// It exists so the plant matrix can load a FRESH, unmutated copy for every
// mutation instead of mutating a shared one and undoing it afterwards. An undo
// that is subtly incomplete makes every subsequent plant meaningless, and it
// makes it meaningless quietly.
func LoadFS(fsys fs.FS) (*Fixture, error) {
	f := &Fixture{}
	var err error

	if err := readJSON(fsys, fileCaptureMeta, &f.Meta); err != nil {
		return nil, err
	}

	// The CometBFT types carry their own JSON representation (registered
	// interface types for public keys, among others), so they are decoded with
	// CometBFT's codec rather than encoding/json.
	var shRaw json.RawMessage
	if err := readJSON(fsys, fileSignedHeader, &shRaw); err != nil {
		return nil, err
	}
	var sh types.SignedHeader
	if err := cmtjson.Unmarshal(shRaw, &sh); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrMalformedFixture, fileSignedHeader, err)
	}
	f.SignedHeader = &sh

	if f.Validators, err = readValidatorSet(fsys, fileValidators); err != nil {
		return nil, err
	}
	if f.ForeignValidators, err = readValidatorSet(fsys, fileForeignValidators); err != nil {
		return nil, err
	}

	f.Membership = &ProofBundle{}
	if err := readJSON(fsys, fileMembership, f.Membership); err != nil {
		return nil, err
	}
	f.NonMembership = &ProofBundle{}
	if err := readJSON(fsys, fileNonMembership, f.NonMembership); err != nil {
		return nil, err
	}

	return f, nil
}

// readValidatorSet decodes one captured validator array into a set.
//
// It uses ValidatorSetFromExistingValidators rather than NewValidatorSet
// because that constructor rebuilds the set WITHOUT touching voting power or
// proposer priority. NewValidatorSet recomputes priorities, which is correct
// when you are advancing a set and wrong when you are reconstructing one that
// already existed.
func readValidatorSet(fsys fs.FS, name string) (*types.ValidatorSet, error) {
	var raw json.RawMessage
	if err := readJSON(fsys, name, &raw); err != nil {
		return nil, err
	}
	var vals []*types.Validator
	if err := cmtjson.Unmarshal(raw, &vals); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrMalformedFixture, name, err)
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("%w: %s holds no validators", ErrMalformedFixture, name)
	}
	set, err := types.ValidatorSetFromExistingValidators(vals)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrMalformedFixture, name, err)
	}
	return set, nil
}

// readJSON reads one file out of the fixture directory.
func readJSON(fsys fs.FS, name string, into any) error {
	b, err := fs.ReadFile(fsys, path.Join(testdataDir, name))
	if err != nil {
		return fmt.Errorf("%w: read %s: %w", ErrMalformedFixture, name, err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		return fmt.Errorf("%w: parse %s: %w", ErrMalformedFixture, name, err)
	}
	return nil
}

// AppHash is the application-state root this fixture's header commits to. Both
// proof bundles are checked against it.
func (f *Fixture) AppHash() []byte { return f.SignedHeader.Header.AppHash }

// VerifyCommit checks the captured header against the captured validator set.
func (f *Fixture) VerifyCommit() (CommitResult, error) {
	return VerifyCommit(f.Meta.ChainID, f.Validators, f.SignedHeader)
}

// VerifyMembership checks the captured existence proof against the AppHash.
func (f *Fixture) VerifyMembership() error {
	return VerifyMembership(f.AppHash(), f.Membership)
}

// VerifyNonMembership checks the captured absence proof against the AppHash.
func (f *Fixture) VerifyNonMembership() error {
	return VerifyNonMembership(f.AppHash(), f.NonMembership)
}
