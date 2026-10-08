package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func corpusPath(parts ...string) string {
	return filepath.Join(append([]string{"..", "..", "corpus"}, parts...)...)
}

func corpusBytes(t *testing.T, parts ...string) []byte {
	t.Helper()
	raw, err := os.ReadFile(corpusPath(parts...))
	if err != nil {
		t.Fatalf("read corpus fixture: %v", err)
	}
	return raw
}

// --- corpus decode: the vendored goldens are the contract ---

func TestCorpusManifestDecodes(t *testing.T) {
	var m Manifest
	if err := json.Unmarshal(corpusBytes(t, "manifest.json"), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if m.Organization.Name != "Corpus Primary Organization" {
		t.Errorf("organization = %+v", m.Organization)
	}
	if m.User == nil || m.User.Email != "developer@corpus.usefulcrum.test" {
		t.Errorf("user = %+v", m.User)
	}
	if m.API == nil || m.API.Contract != 1 {
		t.Fatalf("api block = %+v", m.API)
	}
	for _, capability := range []string{
		"skills", "proposals", "proposals_index", "projects", "architecture",
		"org_skills", "skill_proposals", "skill_drafts",
	} {
		if !m.API.Has(capability) {
			t.Errorf("missing capability %q", capability)
		}
	}
	if m.API.Has("time-travel") {
		t.Error("Has must not invent capabilities")
	}
	if m.API.Download != "https://usefulcrum.ai/cli" {
		t.Errorf("download = %q", m.API.Download)
	}

	if len(m.Documents) != 3 {
		t.Fatalf("documents = %d", len(m.Documents))
	}
	bySlug := map[string]ManifestDocument{}
	for _, d := range m.Documents {
		bySlug[d.Slug] = d
	}
	md, source := bySlug["estimation-rubric"], bySlug["estimation-rubric-source"]
	if md.ProposalSlug != nil {
		t.Errorf("generated markdown must not be proposable, got %q", *md.ProposalSlug)
	}
	if source.ProposalSlug == nil || *source.ProposalSlug != "estimation-rubric" {
		t.Errorf("source proposal_slug = %v", source.ProposalSlug)
	}
	// The markdown is a RENDERING, not the document: the renderer merges in
	// values the document does not carry, such as measured calibration
	// multipliers. It is identified by the digest of its own body, so a
	// rendering change moves the digest — under the old shared digest, a
	// rendering could change while every client still answered "synced" and
	// withheld the methodology update. The JSON source keeps the document's
	// own digest, because there the document IS the artifact.
	if md.Digest == "" || source.Digest == "" {
		t.Errorf("digests must be present: md=%q source=%q", md.Digest, source.Digest)
	}
	if md.Digest == source.Digest {
		t.Error("rendering and document must be identified separately, got one digest for both")
	}

	// Org skills: markdown that IS the source, flat filename, self proposal_slug.
	skill := bySlug["skill-corpus-writing-specs"]
	if skill.Format != "markdown" || skill.Filename != "skill-corpus-writing-specs.md" {
		t.Errorf("skill doc = %+v", skill)
	}
	if skill.ProposalSlug == nil || *skill.ProposalSlug != "skill-corpus-writing-specs" {
		t.Errorf("skill proposal_slug = %v", skill.ProposalSlug)
	}
}

func TestCorpusErrorBodiesDecode(t *testing.T) {
	cases := []struct {
		fixture string
		code    string
	}{
		{"unauthorized.json", "unauthorized"},
		{"organization_required.json", "organization_required"},
		{"unknown_document.json", "unknown_document"},
		{"unknown_feature.json", "unknown_feature"},
		{"unknown_project.json", "unknown_project"},
		{"unknown_proposal.json", "unknown_proposal"},
	}
	for _, tc := range cases {
		resp := &http.Response{StatusCode: 422, Header: http.Header{}}
		apiErr := newError("GET", "/x", resp, corpusBytes(t, "errors", tc.fixture))
		if apiErr.Code != tc.code {
			t.Errorf("%s code = %q, want %q", tc.fixture, apiErr.Code, tc.code)
		}
		if apiErr.ServerMessage == "" {
			t.Errorf("%s lost the human message", tc.fixture)
		}
	}

	// The org-required 422 carries a structured org list for pickers.
	var orgRequired struct {
		Organizations []Organization `json:"organizations"`
	}
	if err := json.Unmarshal(corpusBytes(t, "errors", "organization_required.json"), &orgRequired); err != nil {
		t.Fatal(err)
	}
	if len(orgRequired.Organizations) != 2 {
		t.Errorf("organizations = %+v", orgRequired.Organizations)
	}
}

// corpus.sha256 is the drift tripwire: if the vendored files do not hash to
// the listing, the corpus was edited by hand or half-updated.
func TestCorpusIntegrity(t *testing.T) {
	listing := strings.TrimRight(string(corpusBytes(t, "corpus.sha256")), "\n")
	for _, line := range strings.Split(listing, "\n") {
		digest, name, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("malformed corpus.sha256 line %q", line)
		}
		sum := sha256.Sum256(corpusBytes(t, filepath.FromSlash(name)))
		if hex.EncodeToString(sum[:]) != digest {
			t.Errorf("%s does not match corpus.sha256 — re-vendor from bin/rails api_contract:regenerate", name)
		}
	}
}

// Unknown fields must never break decoding — additive server changes are the
// forward-compatibility contract.
func TestUnknownFieldTolerance(t *testing.T) {
	raw := `{"organization":{"id":1,"name":"x","plan":"gold"},"api":{"contract":1,"new_thing":true},
		"documents":[{"slug":"s","format":"json","digest":"d","filename":"f","brand_new":"y"}],"extra_top":{}}`
	var m Manifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unknown fields must be tolerated: %v", err)
	}
	if m.Documents[0].Slug != "s" {
		t.Errorf("decode lost known fields: %+v", m.Documents[0])
	}
}

// --- httptest: wire-level behavior ---

func newTestClient(handler http.Handler) (*Client, *httptest.Server) {
	server := httptest.NewServer(handler)
	client := &Client{BaseURL: server.URL, Token: "tok-123", OrganizationID: "42", Version: "1.2.3"}
	return client, server
}

func TestGetSendsAuthOrgAndIdentity(t *testing.T) {
	var got *http.Request
	client, server := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		w.Write(corpusBytes(t, "manifest.json"))
	}))
	defer server.Close()

	if _, _, err := client.Manifest(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if got.Header.Get("Authorization") != "Bearer tok-123" {
		t.Errorf("auth header = %q", got.Header.Get("Authorization"))
	}
	if got.URL.Query().Get("organization_id") != "42" {
		t.Errorf("org must ride the query on GETs, got %q", got.URL.RawQuery)
	}
	if ua := got.Header.Get("User-Agent"); !strings.HasPrefix(ua, "fulcrum/1.2.3 (") {
		t.Errorf("user-agent = %q", ua)
	}
	if got.Header.Get("X-Fulcrum-Client") != "fulcrum/1.2.3" {
		t.Errorf("client identity = %q", got.Header.Get("X-Fulcrum-Client"))
	}
}

func TestDocumentReturnsRawBytes(t *testing.T) {
	markdown := corpusBytes(t, "documents", "estimation-rubric.md")
	client, server := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent_context/skills/estimation-rubric" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/markdown")
		w.Write(markdown)
	}))
	defer server.Close()

	res, err := client.Document(context.Background(), "estimation-rubric", "")
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Body) != string(markdown) {
		t.Error("document bytes must be verbatim")
	}
}

func TestPostPutsOrgInBody(t *testing.T) {
	var body map[string]any
	client, server := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.RawQuery != "" {
			t.Errorf("POSTs must not put org in the query, got %q", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":7,"status":"pending","based_on_current":true,"review_url":"/knowledge_proposals/7"}`)
	}))
	defer server.Close()

	receipt, err := client.SubmitProposal(context.Background(), "estimation-rubric",
		map[string]any{"rubric_id": "x"}, "digest", "note")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ID != 7 || !receipt.BasedOnCurrent {
		t.Errorf("receipt = %+v", receipt)
	}
	if body["organization_id"] != "42" {
		t.Errorf("org must ride the body on POSTs, got %v", body["organization_id"])
	}
	if body["slug"] != "estimation-rubric" || body["note"] != "note" || body["base_digest"] != "digest" {
		t.Errorf("payload = %v", body)
	}
}

func TestNotModified(t *testing.T) {
	client, server := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"etag-1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"etag-1"`)
		w.Write(corpusBytes(t, "manifest.json"))
	}))
	defer server.Close()

	manifest, res, err := client.Manifest(context.Background(), "")
	if err != nil || manifest == nil {
		t.Fatalf("first fetch: %v", err)
	}
	if res.ETag != `"etag-1"` {
		t.Fatalf("etag = %q", res.ETag)
	}

	manifest, res, err = client.Manifest(context.Background(), res.ETag)
	if err != nil {
		t.Fatal(err)
	}
	if manifest != nil || !res.NotModified {
		t.Errorf("304 must surface as NotModified, got manifest=%v res=%+v", manifest, res)
	}
}

func TestErrorSurfacesServerBody(t *testing.T) {
	client, server := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write(corpusBytes(t, "errors", "organization_required.json"))
	}))
	defer server.Close()

	_, _, err := client.Manifest(context.Background(), "")
	apiErr, ok := AsError(err)
	if !ok {
		t.Fatalf("want *Error, got %T %v", err, err)
	}
	if apiErr.Status != 422 || apiErr.Code != "organization_required" {
		t.Errorf("error = %+v", apiErr)
	}
	if !strings.Contains(apiErr.Body, "organizations") {
		t.Error("server body must be preserved verbatim")
	}
}

func TestRateLimitCarriesRetryAfter(t *testing.T) {
	client, server := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "300")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":"rate limited; retry after 300s","code":"rate_limited"}`)
	}))
	defer server.Close()

	_, _, err := client.Manifest(context.Background(), "")
	apiErr, ok := AsError(err)
	if !ok || apiErr.Status != 429 || apiErr.Code != "rate_limited" || apiErr.RetryAfter != "300" {
		t.Errorf("429 = %+v (ok=%v)", apiErr, ok)
	}
}

func TestUpgradeRequired(t *testing.T) {
	client, server := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUpgradeRequired)
		fmt.Fprint(w, `{"error":"client 0.1.0 is below the minimum supported 1.0.0; upgrade: https://usefulcrum.ai/cli","code":"upgrade_required","min_client":"1.0.0"}`)
	}))
	defer server.Close()

	_, _, err := client.Manifest(context.Background(), "")
	apiErr, ok := AsError(err)
	if !ok || apiErr.Status != 426 || apiErr.Code != "upgrade_required" {
		t.Errorf("426 = %+v (ok=%v)", apiErr, ok)
	}
}

func TestTransportErrorIsNotContractError(t *testing.T) {
	client := &Client{BaseURL: "http://127.0.0.1:1", Token: "t"} // nothing listens on port 1
	_, _, err := client.Manifest(context.Background(), "")
	if err == nil {
		t.Fatal("want a transport error")
	}
	if _, ok := AsError(err); ok {
		t.Errorf("transport failures must not be *Error: %v", err)
	}
}

func TestInsecureBaseURL(t *testing.T) {
	cases := map[string]bool{
		"https://usefulcrum.ai": false,
		"http://localhost:3100": false,
		"http://127.0.0.1:3000": false,
		"http://fulcrum.lan":    true,
		"http://usefulcrum.ai":  true,
		"not a url":             false,
	}
	for base, want := range cases {
		if got := InsecureBaseURL(base); got != want {
			t.Errorf("InsecureBaseURL(%q) = %v, want %v", base, got, want)
		}
	}
}

// The rendering's digest must verify the file a client synced.
//
// This is the property the shared digest used to hide: a rendering could change
// while the manifest row did not, so `classify` answered "synced" against a
// stale file and withheld exactly the methodology updates a dev machine most
// needs. The digest covers the rendering BODY only — never the frontmatter,
// which carries generated_at and would report drift on every regeneration.
func TestRubricRenderingDigestVerifiesTheFile(t *testing.T) {
	var m Manifest
	if err := json.Unmarshal(corpusBytes(t, "manifest.json"), &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	var row ManifestDocument
	for _, d := range m.Documents {
		if d.Slug == "estimation-rubric" {
			row = d
		}
	}
	if row.Digest == "" {
		t.Fatal("no estimation-rubric row in the manifest")
	}

	markdown := string(corpusBytes(t, "documents", "estimation-rubric.md"))
	frontmatter, body, ok := splitFrontmatter(markdown)
	if !ok {
		t.Fatal("rubric markdown has no frontmatter")
	}

	sum := sha256.Sum256([]byte(body))
	if got := hex.EncodeToString(sum[:]); got != row.Digest {
		t.Errorf("digest of the rendering body = %s, manifest says %s", got, row.Digest)
	}

	// Stated in the frontmatter too, so a synced file can be checked on its own
	// without the manifest that advertised it.
	if stated := frontmatterValue(frontmatter, "digest"); stated != row.Digest {
		t.Errorf("frontmatter digest = %q, manifest says %q", stated, row.Digest)
	}

	var source ManifestDocument
	for _, d := range m.Documents {
		if d.Slug == "estimation-rubric-source" {
			source = d
		}
	}
	if stated := frontmatterValue(frontmatter, "rubric_digest"); stated != source.Digest {
		t.Errorf("frontmatter rubric_digest = %q, source row says %q", stated, source.Digest)
	}
}

// splitFrontmatter returns the YAML block and the rendering after it.
//
// The server writes frontmatter, then a blank line, then the rendering — and
// digests the rendering alone. So the single separating newline is dropped
// here; leaving it in changes the hash.
func splitFrontmatter(doc string) (frontmatter, body string, ok bool) {
	if !strings.HasPrefix(doc, "---\n") {
		return "", "", false
	}
	rest := doc[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return "", "", false
	}
	return rest[:end], strings.TrimPrefix(rest[end+len("\n---\n"):], "\n"), true
}

func frontmatterValue(frontmatter, key string) string {
	for _, line := range strings.Split(frontmatter, "\n") {
		name, value, found := strings.Cut(line, ":")
		if found && strings.TrimSpace(name) == key {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func TestCorpusTelemetryReceiptDecodes(t *testing.T) {
	var receipt TelemetryReceipt
	if err := json.Unmarshal(corpusBytes(t, "telemetry", "receipt.json"), &receipt); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if receipt.FeatureID == 0 || receipt.Recorded == 0 || receipt.Tokens["total"] == 0 {
		t.Errorf("receipt = %+v", receipt)
	}
}

// --- POST /mcp: JSON-RPC 2.0, pinned by corpus/mcp-rpc ---

// Every mcp-rpc golden is a JSON-RPC 2.0 response carrying exactly one of
// result or error — including initialize.json, which this client never sends
// because the server is stateless, but which pins the envelope all the same.
func TestCorpusMcpRpcEnvelopes(t *testing.T) {
	entries, err := os.ReadDir(corpusPath("mcp-rpc"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no mcp-rpc fixtures vendored")
	}
	for _, entry := range entries {
		var envelope rpcResponse
		if err := json.Unmarshal(corpusBytes(t, "mcp-rpc", entry.Name()), &envelope); err != nil {
			t.Errorf("%s: %v", entry.Name(), err)
			continue
		}
		hasResult := len(envelope.Result) > 0 && string(envelope.Result) != "null"
		if envelope.JSONRPC != "2.0" || hasResult == (envelope.Error != nil) {
			t.Errorf("%s: jsonrpc=%q result=%v error=%v", entry.Name(), envelope.JSONRPC, hasResult, envelope.Error)
		}
	}
}

// rpcRequest is what the client put on the wire.
type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int64          `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

// serveRPC answers every request with a corpus fixture, re-stamped with the
// request's id the way the live server echoes it. The fixtures were generated
// with id 1.
func serveRPC(t *testing.T, fixture string, seen *[]rpcRequest, inspect func(*http.Request)) (*Client, *httptest.Server) {
	t.Helper()
	golden := string(corpusBytes(t, "mcp-rpc", fixture))
	return newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inspect != nil {
			inspect(r)
		}
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		if seen != nil {
			*seen = append(*seen, req)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, strings.Replace(golden, `"id":1,`, fmt.Sprintf(`"id":%d,`, req.ID), 1))
	}))
}

func TestMcpToolsPostsToolsListToMcp(t *testing.T) {
	var got *http.Request
	var seen []rpcRequest
	client, server := serveRPC(t, "tools-list.json", &seen, func(r *http.Request) {
		got = r.Clone(context.Background())
	})
	defer server.Close()

	tools, err := client.McpTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if got.Method != http.MethodPost || got.URL.Path != "/mcp" {
		t.Errorf("request = %s %s, want POST /mcp", got.Method, got.URL.Path)
	}
	if got.URL.Query().Get("organization_id") != "42" {
		t.Errorf("org must ride the query on POST /mcp, got %q", got.URL.RawQuery)
	}
	if got.Header.Get("Authorization") != "Bearer tok-123" {
		t.Errorf("auth header = %q", got.Header.Get("Authorization"))
	}
	// The 426 kill switch and the usage report both key on this.
	if got.Header.Get("X-Fulcrum-Client") != "fulcrum/1.2.3" {
		t.Errorf("client identity = %q", got.Header.Get("X-Fulcrum-Client"))
	}
	if got.Header.Get("Content-Type") != "application/json" {
		t.Errorf("content-type = %q", got.Header.Get("Content-Type"))
	}

	req := seen[0]
	if req.JSONRPC != "2.0" || req.Method != "tools/list" || req.ID == 0 {
		t.Errorf("envelope = %+v", req)
	}
	if req.Params == nil || len(req.Params) != 0 {
		t.Errorf("tools/list params = %v, want {}", req.Params)
	}

	byName := map[string]ToolDefinition{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	feature, ok := byName["get_feature"]
	if !ok {
		t.Fatalf("get_feature missing from %d tools", len(tools))
	}
	if feature.Description == "" || feature.InputSchema["type"] != "object" {
		t.Errorf("get_feature = %+v", feature)
	}
}

func TestMcpCallPostsToolsCallToMcp(t *testing.T) {
	var seen []rpcRequest
	var rawQuery string
	client, server := serveRPC(t, "tools-call.json", &seen, func(r *http.Request) {
		rawQuery = r.URL.RawQuery
	})
	defer server.Close()

	result, err := client.McpCall(context.Background(), "list_projects", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !strings.Contains(result.Text(), "Corpus Project") {
		t.Errorf("result = %+v", result)
	}

	req := seen[0]
	if req.Method != "tools/call" || req.Params["name"] != "list_projects" {
		t.Errorf("envelope = %+v", req)
	}
	if arguments, ok := req.Params["arguments"].(map[string]any); !ok || len(arguments) != 0 {
		t.Errorf("nil arguments must go out as {}, got %#v", req.Params["arguments"])
	}
	if _, inParams := req.Params["organization_id"]; inParams {
		t.Error("organization_id must not ride the JSON-RPC body")
	}
	if rawQuery != "organization_id=42" {
		t.Errorf("query = %q", rawQuery)
	}
}

func TestMcpRequestIdsIncrease(t *testing.T) {
	var seen []rpcRequest
	client, server := serveRPC(t, "tools-call.json", &seen, nil)
	defer server.Close()

	for range 3 {
		if _, err := client.McpCall(context.Background(), "list_projects", nil); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < len(seen); i++ {
		if seen[i].ID <= seen[i-1].ID {
			t.Errorf("ids = %d then %d, want increasing", seen[i-1].ID, seen[i].ID)
		}
	}
}

// A tool failure is a RESULT for the model, never an error.
func TestMcpCallToolFailuresAreResults(t *testing.T) {
	cases := []struct {
		fixture string
		prefix  string
	}{
		{"tool-error.json", "No card matching"},
		// No structured org list on this door: the text names the choices.
		{"organization-required.json", "organization required:"},
	}
	for _, tc := range cases {
		client, server := serveRPC(t, tc.fixture, nil, nil)
		result, err := client.McpCall(context.Background(), "get_feature", map[string]any{"feature": "NOPE-999999"})
		server.Close()
		if err != nil {
			t.Errorf("%s: want a result, got error %v", tc.fixture, err)
			continue
		}
		if !result.IsError || !strings.HasPrefix(result.Text(), tc.prefix) {
			t.Errorf("%s: result = %+v", tc.fixture, result)
		}
	}
}

// A JSON-RPC error object in a 200 is a protocol fault: an error, and a
// distinct type from an HTTP refusal.
func TestMcpJSONRPCErrorIsAnError(t *testing.T) {
	client, server := serveRPC(t, "method-not-found.json", nil, nil)
	defer server.Close()

	result, err := client.McpCall(context.Background(), "get_feature", nil)
	if result != nil {
		t.Errorf("a protocol fault must not produce a result, got %+v", result)
	}
	rpcErr, ok := AsRPCError(err)
	if !ok {
		t.Fatalf("want *RPCError, got %T %v", err, err)
	}
	if rpcErr.Code != -32601 || rpcErr.Method != "tools/call" || !strings.Contains(rpcErr.Message, "does not implement") {
		t.Errorf("rpc error = %+v", rpcErr)
	}
	if _, isHTTP := AsError(err); isHTTP {
		t.Error("a JSON-RPC error must not masquerade as an HTTP error")
	}
}

func TestMcpResponseForAnotherRequestIsRejected(t *testing.T) {
	client, server := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(corpusBytes(t, "mcp-rpc", "tools-call.json")) // always id 1
	}))
	defer server.Close()

	if _, err := client.McpCall(context.Background(), "list_projects", nil); err != nil {
		t.Fatalf("first call carries id 1: %v", err)
	}
	if _, err := client.McpCall(context.Background(), "list_projects", nil); err == nil {
		t.Error("an answer to request 1 must not be accepted for request 2")
	}
}

// HTTP refusals still happen before JSON-RPC dispatch and keep their
// *Error mapping.
func TestMcpHTTPRefusalsStayContractErrors(t *testing.T) {
	cases := []struct {
		status     int
		body       string
		code       string
		retryAfter string
	}{
		{401, string(corpusBytes(t, "errors", "unauthorized.json")), "unauthorized", ""},
		{426, `{"error":"client 0.1.0 is below the minimum supported 1.0.0; upgrade: https://usefulcrum.ai/cli","code":"upgrade_required","min_client":"1.0.0"}`, "upgrade_required", ""},
		{429, `{"error":"rate limited; retry after 60s","code":"rate_limited"}`, "rate_limited", "60"},
	}
	for _, tc := range cases {
		client, server := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tc.retryAfter != "" {
				w.Header().Set("Retry-After", tc.retryAfter)
			}
			w.WriteHeader(tc.status)
			fmt.Fprint(w, tc.body)
		}))
		_, err := client.McpTools(context.Background())
		server.Close()

		apiErr, ok := AsError(err)
		if !ok {
			t.Errorf("%d: want *Error, got %T %v", tc.status, err, err)
			continue
		}
		if apiErr.Status != tc.status || apiErr.Code != tc.code || apiErr.RetryAfter != tc.retryAfter || apiErr.Path != "/mcp" {
			t.Errorf("%d: error = %+v", tc.status, apiErr)
		}
	}
}
