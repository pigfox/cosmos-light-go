package cosmoslight

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// THE ONLY CODE IN THIS REPOSITORY THAT OPENS A SOCKET, and it lives in a test
// file so it is not even compiled into a consumer's binary. Verification is
// pure computation over bytes on disk; that is checked by
// TestNoNetworkImportsOutsideCapture in verify_test.go rather than asserted
// here in prose.
//
// It is skipped unless COSMOS_LIGHT_CAPTURE=1. Recapturing replaces every file
// under testdata and therefore invalidates every height, hash and figure quoted
// in the README and in any consumer — it is a deliberate act, not something a
// routine `go test ./...` should ever do by accident.
//
// It VERIFIES BEFORE IT WRITES. A capture that persists fixtures it has not
// checked would hand the plant matrix inputs that fail for reasons having
// nothing to do with the plants, and the matrix would look like it was working.

func TestCapture(t *testing.T) {
	if os.Getenv(captureEnabledEnv) != captureEnabledValue {
		t.Skipf("set %s=%s to re-capture testdata from a live node", captureEnabledEnv, captureEnabledValue)
	}

	endpoint := os.Getenv(captureEndpointEnv)
	if endpoint == "" {
		endpoint = defaultCaptureEndpoint
	}
	c := &captureClient{
		base: endpoint,
		http: &http.Client{Timeout: captureHTTPTimeoutSeconds * time.Second},
		t:    t,
	}

	// 1. Where are we, and how far along?
	var status struct {
		NodeInfo struct {
			Network string `json:"network"`
			Version string `json:"version"`
		} `json:"node_info"`
		SyncInfo struct {
			LatestBlockHeight   string `json:"latest_block_height"`
			EarliestBlockHeight string `json:"earliest_block_height"`
			CatchingUp          bool   `json:"catching_up"`
		} `json:"sync_info"`
	}
	c.get("/status", nil, &status)
	if status.SyncInfo.CatchingUp {
		t.Fatalf("%s is still catching up; its recent state is not final", endpoint)
	}
	latest, err := strconv.ParseInt(status.SyncInfo.LatestBlockHeight, 10, 64)
	if err != nil {
		t.Fatalf("latest_block_height %q is not a number: %v", status.SyncInfo.LatestBlockHeight, err)
	}

	earliest, err := strconv.ParseInt(status.SyncInfo.EarliestBlockHeight, 10, 64)
	if err != nil {
		t.Fatalf("earliest_block_height %q is not a number: %v", status.SyncInfo.EarliestBlockHeight, err)
	}

	// The state is READ at queryHeight and COMMITTED BY the header one higher.
	queryHeight := latest - captureHeightLag
	headerHeight := queryHeight + 1
	foreignHeight := earliest + foreignValidatorMargin
	if foreignHeight >= queryHeight {
		t.Fatalf("the node retains only %d..%d, which is too narrow to read a validator set "+
			"from a genuinely different time", earliest, latest)
	}

	meta := CaptureMeta{
		Endpoint:               endpoint,
		ChainID:                status.NodeInfo.Network,
		CometBFTVersion:        status.NodeInfo.Version,
		CapturedAtUTC:          time.Now().UTC().Format(time.RFC3339),
		QueryHeight:            queryHeight,
		HeaderHeight:           headerHeight,
		ForeignValidatorHeight: foreignHeight,
		Store:                  captureStore,
	}
	t.Logf("capturing from %s: chain %s, cometbft %s, query height %d, header height %d",
		endpoint, meta.ChainID, meta.CometBFTVersion, queryHeight, headerHeight)

	// 2. The signed header that commits to that state.
	var commitResult struct {
		SignedHeader json.RawMessage `json:"signed_header"`
	}
	c.get("/commit", url.Values{"height": {strconv.FormatInt(headerHeight, 10)}}, &commitResult)

	// 3. The validator set in force at that header, paginated, plus one from a
	//    genuinely different time for the plant matrix to substitute.
	validators := c.allValidators(headerHeight)
	foreignValidators := c.allValidators(foreignHeight)

	// 4. The two state proofs, both read at queryHeight.
	membership := c.storeProof(queryHeight, supplyKey(presentDenom))
	if membership.Value == nil {
		t.Fatalf("the %q supply key returned no value; it cannot anchor an existence proof", presentDenom)
	}
	nonMembership := c.storeProof(queryHeight, supplyKey(absentDenom))
	if nonMembership.Value != nil {
		t.Fatalf("the %q supply key returned a value, so it is not absent and cannot anchor a "+
			"non-existence proof", absentDenom)
	}

	// 5. Write to a temporary directory and verify THAT, before touching
	//    testdata. A capture is only allowed to replace known-good fixtures
	//    with fixtures that have themselves been checked.
	// The staging tree mirrors the real layout — fixtures live UNDER testdata/,
	// because that is the path LoadFS reads and a staging check against a
	// different layout would be checking something the promoted copy is not.
	staging := t.TempDir()
	writeCapture(t, filepath.Join(staging, testdataDir), meta, commitResult.SignedHeader, validators, foreignValidators, membership, nonMembership)

	f, err := LoadFS(os.DirFS(staging))
	if err != nil {
		t.Fatalf("the freshly captured fixture does not load: %v", err)
	}
	res, err := f.VerifyCommit()
	if err != nil {
		t.Fatalf("the freshly captured commit does not verify: %v", err)
	}
	if err := f.VerifyMembership(); err != nil {
		t.Fatalf("the freshly captured existence proof does not verify: %v", err)
	}
	if err := f.VerifyNonMembership(); err != nil {
		t.Fatalf("the freshly captured absence proof does not verify: %v", err)
	}
	t.Logf("verified before writing: %d/%d validators signed, %d/%d voting power (needed more than %d)",
		res.SignedCount, res.ValidatorCount, res.SignedVotingPower, res.TotalVotingPower, res.VotingPowerNeeded)

	// The foreign set must actually BE foreign. Validator sets change rarely,
	// so a distant height can easily return the identical set — and if it did,
	// plant P7 would pass while proving nothing at all.
	if bytes.Equal(f.ForeignValidators.Hash(), f.Validators.Hash()) {
		t.Fatalf("the validator set at height %d hashes identically to the one at %d, so it "+
			"cannot stand in as a wrong set; pick a further height",
			foreignHeight, headerHeight)
	}
	t.Logf("foreign validator set at height %d: %d validators, hash %X (header commits to %X)",
		foreignHeight, f.ForeignValidators.Size(), f.ForeignValidators.Hash(), f.Validators.Hash())

	// 6. Only now, promote.
	writeCapture(t, testdataDir, meta, commitResult.SignedHeader, validators, foreignValidators, membership, nonMembership)
	t.Logf("wrote %s/", testdataDir)
}

// supplyKey builds the bank module's supply key for a denomination.
func supplyKey(denom string) []byte {
	return append([]byte{bankSupplyKeyPrefix}, denom...)
}

type captureClient struct {
	base string
	http *http.Client
	t    *testing.T
}

// get performs one RPC call and unwraps the JSON-RPC envelope.
func (c *captureClient) get(path string, q url.Values, into any) {
	c.t.Helper()

	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil) //nolint:noctx // bounded by the client's timeout
	if err != nil {
		c.t.Fatalf("build request for %s: %v", u, err)
	}
	// AN EXPLICIT IDENTITY, because the default one is refused. Public RPC
	// endpoints sit behind edge proxies that drop Go's stock User-Agent: the
	// same URL that curl fetches happily answers the default client with a bare
	// EOF during the TLS exchange, which reads like a network fault rather than
	// a policy decision. Naming the client is both the fix and the courteous
	// thing to do to an endpoint someone else pays for.
	req.Header.Set("User-Agent", captureUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("read %s: %v", u, err)
	}
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("GET %s: HTTP %d: %s", u, resp.StatusCode, body)
	}

	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
			Data    string `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		c.t.Fatalf("parse %s: %v", u, err)
	}
	if env.Error != nil {
		c.t.Fatalf("GET %s: RPC error: %s %s", u, env.Error.Message, env.Error.Data)
	}
	if err := json.Unmarshal(env.Result, into); err != nil {
		c.t.Fatalf("parse result of %s: %v", u, err)
	}
}

// allValidators pages through /validators and returns the raw array.
//
// It refuses a SHORT read: the endpoint reports how many validators the height
// has, and a set that silently stopped early would hash to something other than
// the header's ValidatorsHash — a confusing failure two steps downstream from
// its cause.
func (c *captureClient) allValidators(height int64) json.RawMessage {
	c.t.Helper()

	var all []json.RawMessage
	total := -1
	for page := 1; ; page++ {
		var res struct {
			Validators []json.RawMessage `json:"validators"`
			Total      string            `json:"total"`
		}
		c.get("/validators", url.Values{
			"height":   {strconv.FormatInt(height, 10)},
			"page":     {strconv.Itoa(page)},
			"per_page": {strconv.Itoa(validatorsPerPage)},
		}, &res)

		if total < 0 {
			parsed, err := strconv.Atoi(res.Total)
			if err != nil {
				c.t.Fatalf("validators total %q is not a number: %v", res.Total, err)
			}
			total = parsed
		}
		all = append(all, res.Validators...)
		if len(res.Validators) == 0 || len(all) >= total {
			break
		}
	}
	if len(all) != total {
		c.t.Fatalf("read %d validators at height %d, the node reports %d", len(all), height, total)
	}

	out, err := json.Marshal(all)
	if err != nil {
		c.t.Fatalf("re-encode validators: %v", err)
	}
	return out
}

// storeProof runs one proving ABCI query against a module store.
func (c *captureClient) storeProof(height int64, key []byte) *ProofBundle {
	c.t.Helper()

	var res struct {
		Response struct {
			Code     uint32 `json:"code"`
			Log      string `json:"log"`
			Height   string `json:"height"`
			Key      []byte `json:"key"`
			Value    []byte `json:"value"`
			ProofOps *struct {
				Ops []struct {
					Type string `json:"type"`
					Key  []byte `json:"key"`
					Data []byte `json:"data"`
				} `json:"ops"`
			} `json:"proofOps"`
		} `json:"response"`
	}
	c.get("/abci_query", url.Values{
		"path":   {fmt.Sprintf("%q", "/store/"+captureStore+"/key")},
		"data":   {"0x" + hexOf(key)},
		"height": {strconv.FormatInt(height, 10)},
		"prove":  {"true"},
	}, &res)

	if res.Response.Code != 0 {
		c.t.Fatalf("abci_query returned code %d: %s", res.Response.Code, res.Response.Log)
	}
	if res.Response.ProofOps == nil {
		c.t.Fatal("abci_query returned no proof operations; the node may not be serving proofs")
	}

	got, err := strconv.ParseInt(res.Response.Height, 10, 64)
	if err != nil {
		c.t.Fatalf("abci_query height %q is not a number: %v", res.Response.Height, err)
	}
	if got != height {
		c.t.Fatalf("asked for height %d, the node answered at %d; the proof would be against "+
			"a different state root than the captured header commits to", height, got)
	}

	b := &ProofBundle{Store: captureStore, QueryHeight: got, Key: res.Response.Key, Value: res.Response.Value}
	for _, op := range res.Response.ProofOps.Ops {
		b.Ops = append(b.Ops, ProofOp{Type: op.Type, Key: op.Key, Data: op.Data})
	}
	return b
}

// hexOf renders bytes as lowercase hex without pulling in a dependency for it.
func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0x0f])
	}
	return string(out)
}

// writeCapture persists one complete capture into dir.
func writeCapture(t *testing.T, dir string, meta CaptureMeta, header, validators, foreignValidators json.RawMessage, membership, nonMembership *ProofBundle) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	write := func(name string, v any) {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatalf("encode %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write(fileCaptureMeta, meta)
	write(fileSignedHeader, header)
	write(fileValidators, validators)
	write(fileForeignValidators, foreignValidators)
	write(fileMembership, membership)
	write(fileNonMembership, nonMembership)
}
