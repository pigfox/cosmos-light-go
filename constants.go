package cosmoslight

// Every threshold, spec name, store name and file name this package depends on
// is named here. Nothing in verify.go carries a bare literal, so a change in
// policy is a change in one place and reads as a decision rather than as a
// typo.

// The commit threshold, as a fraction of TOTAL VOTING POWER.
//
// IT IS A STRICT INEQUALITY, and that is the whole subtlety. CometBFT computes
// `needed := total * 2 / 3` in integer arithmetic and then rejects when
// `tallied <= needed`. So a commit carrying EXACTLY two thirds of the power is
// rejected; it must carry strictly more. The planted-mutation matrix drives
// both sides of that boundary on purpose — a verifier that accepts exactly two
// thirds is broken in a way no happy-path test can see.
const (
	commitPowerNumerator   = 2
	commitPowerDenominator = 3
)

// The ICS-23 proof specs, by the names the ABCI response uses for them.
//
// A Cosmos state proof arrives as an ORDERED PAIR of operations and the order
// is load-bearing: the first proves a key inside one module's IAVL tree, the
// second proves that tree's root inside the multistore. Verifying either
// against the other's spec fails, which is what plant P6 asserts.
const (
	proofOpTypeIAVL   = "ics23:iavl"
	proofOpTypeSimple = "ics23:simple"

	// proofOpCount is how many operations a Cosmos store query returns. Named
	// because a response carrying a different number is a shape this package
	// has never seen and must refuse rather than index into.
	proofOpCount = 2

	// The index of each operation within that pair.
	proofOpIndexIAVL   = 0
	proofOpIndexSimple = 1
)

// The captured fixture files, relative to testdata/.
const (
	fileCaptureMeta   = "capture.json"
	fileSignedHeader  = "signed_header.json"
	fileValidators    = "validators.json"
	fileMembership    = "membership_proof.json"
	fileNonMembership = "nonmembership_proof.json"

	// fileForeignValidators is a validator set from a DIFFERENT height of the
	// same chain. It exists only so plant P7 can substitute a real set rather
	// than a hand-edited one: an edited set proves the hash check notices
	// edits, which is a weaker and less interesting claim than proving it
	// notices a set that genuinely governed the same chain at another time.
	//
	// The capture REFUSES to write it if it hashes the same as the header's
	// own set, because an identical set would make P7 pass by accident.
	fileForeignValidators = "foreign_validators.json"
	testdataDir           = "testdata"
	captureEnabledEnv     = "COSMOS_LIGHT_CAPTURE"
	captureEnabledValue   = "1"
)

// captureEndpointEnv lets the capture test be pointed at a different node
// without editing source. It has no effect on verification, which never reads
// it.
const captureEndpointEnv = "COSMOS_LIGHT_RPC"

// What the capture asks for. These matter only while capturing; verification
// reads the store name and keys back out of the fixture.
const (
	// defaultCaptureEndpoint was resolved from the Cosmos chain registry
	// rather than written from memory, and was confirmed serving before it was
	// recorded here. It is a DEFAULT and not a requirement: a capture can be
	// pointed anywhere with captureEndpointEnv.
	defaultCaptureEndpoint = "https://rpc.osmotest5.osmosis.zone"

	// captureStore is the module whose IAVL tree the proofs come from. The bank
	// module is chosen because its supply record is a key whose existence does
	// not depend on any particular account still being funded.
	captureStore = "bank"

	// captureHeightLag keeps the capture off the chain tip. The state root for
	// height H is committed by the header at H+1, so the tip itself has no
	// header to verify its state against yet.
	captureHeightLag = 8

	// validatorsPerPage is the RPC page size for /validators. The endpoint caps
	// it, and a set larger than one page is normal, so the capture paginates.
	validatorsPerPage = 100

	// foreignValidatorMargin is how far above the node's EARLIEST retained
	// block the foreign validator set is read from. It is expressed against the
	// earliest rather than as a fixed height because public endpoints prune,
	// and a hard-coded old height would start failing the day it fell off.
	foreignValidatorMargin = 10000

	// captureHTTPTimeoutSeconds bounds a single RPC call.
	captureHTTPTimeoutSeconds = 30

	// captureUserAgent identifies this capture to the endpoint. It is not
	// cosmetic: the stock Go user agent is refused outright by the edge proxy
	// in front of at least one public endpoint, and the refusal arrives as an
	// EOF that looks like a broken network.
	captureUserAgent = "cosmos-light-go/0.1 (+https://github.com/pigfox/cosmos-light-go)"
)

// The two keys the capture proves things about, inside the bank module's tree.
//
// bankSupplyKeyPrefix is the Cosmos SDK bank module's supply prefix; the key is
// that byte followed by the denomination. The present denomination is the test
// network's own staking token, which certainly has a supply record. The absent
// one is a denomination that cannot exist, which is what makes a non-existence
// proof meaningful.
const (
	bankSupplyKeyPrefix = 0x00
	presentDenom        = "uosmo"
	absentDenom         = "pf-s209a-no-such-denom"
)
