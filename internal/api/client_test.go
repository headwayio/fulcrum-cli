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
	for _, capability := range []string{"skills", "proposals", "proposals_index", "projects", "architecture"} {
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

	if len(m.Documents) != 2 {
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
	if md.Digest == "" || md.Digest != source.Digest {
		t.Errorf("digests: md=%q source=%q", md.Digest, source.Digest)
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
