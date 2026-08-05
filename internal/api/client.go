// Package api is the typed HTTP client for Fulcrum's /api/agent_context
// contract (contract 1). Bearer auth; organization_id rides the query string
// on GETs and the JSON body on POSTs; server error bodies are preserved
// verbatim — they are the UX. Decoding never rejects unknown fields: that
// tolerance is the forward-compatibility contract.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
)

// Client talks to one Fulcrum server as one token.
type Client struct {
	BaseURL        string
	Token          string
	OrganizationID string
	// Version stamps User-Agent and X-Fulcrum-Client; the server's dormant
	// 426 branch keys on it.
	Version    string
	HTTPClient *http.Client
}

// Error is a non-2xx HTTP response from the server. Contract errors are
// *Error; anything else (DNS, refused connections, timeouts) surfaces as the
// transport's error type — callers branch on that distinction for the
// 0/1/2 exit-code taxonomy.
type Error struct {
	Status        int
	Method        string
	Path          string
	Code          string
	ServerMessage string
	RetryAfter    string
	Body          string
	// Organizations is populated on the organization_required 422 — the
	// server names the choices so a client can offer them rather than make
	// the user go find an id.
	Organizations []Organization
}

func (e *Error) Error() string {
	msg := e.ServerMessage
	if msg == "" {
		msg = truncate(e.Body, 200)
	}
	if e.Code != "" {
		return fmt.Sprintf("%s %s → %d (%s): %s", e.Method, e.Path, e.Status, e.Code, msg)
	}
	return fmt.Sprintf("%s %s → %d: %s", e.Method, e.Path, e.Status, msg)
}

// AsError returns the contract error inside err, if any.
func AsError(err error) (*Error, bool) {
	var apiErr *Error
	ok := errors.As(err, &apiErr)
	return apiErr, ok
}

// Manifest is GET /skills.
type Manifest struct {
	Organization Organization       `json:"organization"`
	User         *User              `json:"user"`
	API          *ContractInfo      `json:"api"`
	GeneratedAt  string             `json:"generated_at"`
	Documents    []ManifestDocument `json:"documents"`
}

type Organization struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type User struct {
	Email string `json:"email"`
}

// ContractInfo is the manifest's api block. Feature-gate on Capabilities,
// never on version comparisons; the contract is additive-only within a
// contract number. Nil on pre-contract servers.
type ContractInfo struct {
	Contract     int      `json:"contract"`
	Capabilities []string `json:"capabilities"`
	MinClient    string   `json:"min_client"`
	LatestClient *string  `json:"latest_client"`
	Download     string   `json:"download"`
}

// Has reports whether the server advertises a capability.
func (c *ContractInfo) Has(capability string) bool {
	if c == nil {
		return false
	}
	for _, has := range c.Capabilities {
		if has == capability {
			return true
		}
	}
	return false
}

type ManifestDocument struct {
	Slug     string `json:"slug"`
	Format   string `json:"format"`
	Digest   string `json:"digest"`
	Version  int    `json:"version"`
	Filename string `json:"filename"`
	// ProposalSlug is nil on documents that cannot be proposed (generated
	// renderings) and on pre-contract servers, which omit the field.
	ProposalSlug *string `json:"proposal_slug"`
	// Draft marks a developer-initiated skill visible only to its creator
	// until a publish proposal reveals it.
	Draft bool `json:"draft"`
}

// SkillDraft is the POST /skills response: a freshly minted, creator-only
// draft, including its template content so the client can write the file
// without a second fetch.
type SkillDraft struct {
	Slug         string `json:"slug"`
	Filename     string `json:"filename"`
	Format       string `json:"format"`
	Digest       string `json:"digest"`
	Version      int    `json:"version"`
	ProposalSlug string `json:"proposal_slug"`
	Draft        bool   `json:"draft"`
	Content      string `json:"content"`
}

// Proposal is one row of GET /proposals (and the body of GET /proposals/:id).
type Proposal struct {
	ID             int64   `json:"id"`
	Slug           string  `json:"slug"`
	Status         string  `json:"status"`
	Note           string  `json:"note"`
	BaseDigest     string  `json:"base_digest"`
	BasedOnCurrent bool    `json:"based_on_current"`
	CreatedAt      string  `json:"created_at"`
	ResolvedAt     *string `json:"resolved_at"`
	ResolvedByName *string `json:"resolved_by_name"`
	// ChangedSections is live-relative and only truthful while pending; the
	// server sends null on resolved rows.
	ChangedSections []string `json:"changed_sections"`
}

// Project is one row of GET /projects.
type Project struct {
	ID                      int64   `json:"id"`
	Name                    string  `json:"name"`
	ClientName              string  `json:"client_name"`
	HasArchitectureProfile  bool    `json:"has_architecture_profile"`
	ArchitectureCollectedAt *string `json:"architecture_collected_at"`
}

// ProjectContext is the GET /projects/:id/context response: the estimation
// bundle plus the snapping table a local estimate must reproduce exactly.
type ProjectContext struct {
	Project struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	Digest   string   `json:"digest"`
	Filename string   `json:"filename"`
	Format   string   `json:"format"`
	Body     string   `json:"body"`
	Fixtures Snapping `json:"snapping_fixtures"`
}

// Snapping is the server's own derivation and snapping rule, published so a
// client implementation can be pinned against it rather than described in
// prose and left to drift.
type Snapping struct {
	Scale           []ScaleStep    `json:"scale"`
	ExpectedFormula string         `json:"expected_formula"`
	Rule            string         `json:"rule"`
	Cases           []SnappingCase `json:"cases"`
}

// ScaleStep is one step of the project's complexity scale.
type ScaleStep struct {
	Label  string  `json:"label"`
	Points int     `json:"points"`
	Hours  float64 `json:"hours"`
}

// SnappingCase is one generated (hours -> label) expectation.
type SnappingCase struct {
	Hours float64 `json:"hours"`
	Label string  `json:"label"`
}

// ProposalReceipt is the POST /proposals response.
type ProposalReceipt struct {
	ID             int64  `json:"id"`
	Status         string `json:"status"`
	BasedOnCurrent bool   `json:"based_on_current"`
	ReviewURL      string `json:"review_url"`
}

// FeaturePushReceipt is the POST /projects/:id/features response.
type FeaturePushReceipt struct {
	ProjectID int64 `json:"project_id"`
	Created   []struct {
		ID             int64  `json:"id"`
		AIFeatureID    string `json:"ai_feature_id"`
		Name           string `json:"name"`
		MoscowPriority string `json:"moscow_priority"`
		Release        string `json:"release"`
		Estimates      []struct {
			Role       string  `json:"role"`
			Complexity string  `json:"complexity"`
			Hours      float64 `json:"hours"`
		} `json:"estimates"`
	} `json:"created"`
	Skipped []struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	} `json:"skipped"`
	// Dropped names estimates the organization's rubric contract rejected.
	// The server drops and reports them; it never repairs them.
	Dropped   []string `json:"dropped"`
	ReviewURL string   `json:"review_url"`
}

// ArchitectureReceipt is the POST /architecture response.
type ArchitectureReceipt struct {
	ProjectID   int64  `json:"project_id"`
	Source      string `json:"source"`
	CollectedAt string `json:"collected_at"`
}

// DocumentResult carries a raw document body plus its cache identity.
type DocumentResult struct {
	Body        []byte
	ETag        string
	NotModified bool
}

// Manifest fetches the document manifest. etag may be "" for an
// unconditional fetch; on a 304 the returned manifest is nil and
// NotModified is true.
func (c *Client) Manifest(ctx context.Context, etag string) (*Manifest, *DocumentResult, error) {
	res, err := c.get(ctx, "/api/agent_context/skills", etag)
	if err != nil {
		return nil, nil, err
	}
	if res.NotModified {
		return nil, res, nil
	}
	manifest := &Manifest{}
	if err := json.Unmarshal(res.Body, manifest); err != nil {
		return nil, nil, fmt.Errorf("decode manifest: %w", err)
	}
	return manifest, res, nil
}

// Document fetches a raw document body (markdown or JSON, verbatim bytes).
func (c *Client) Document(ctx context.Context, slug, etag string) (*DocumentResult, error) {
	return c.get(ctx, "/api/agent_context/skills/"+url.PathEscape(slug), etag)
}

// Proposals lists the token user's own proposals, newest first.
func (c *Client) Proposals(ctx context.Context) ([]Proposal, error) {
	res, err := c.get(ctx, "/api/agent_context/proposals", "")
	if err != nil {
		return nil, err
	}
	var payload struct {
		Proposals []Proposal `json:"proposals"`
	}
	if err := json.Unmarshal(res.Body, &payload); err != nil {
		return nil, fmt.Errorf("decode proposals: %w", err)
	}
	return payload.Proposals, nil
}

// Projects lists the organization's active projects.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	res, err := c.get(ctx, "/api/agent_context/projects", "")
	if err != nil {
		return nil, err
	}
	var payload struct {
		Projects []Project `json:"projects"`
	}
	if err := json.Unmarshal(res.Body, &payload); err != nil {
		return nil, fmt.Errorf("decode projects: %w", err)
	}
	return payload.Projects, nil
}

// SubmitProposal posts a whole-document proposal.
func (c *Client) SubmitProposal(ctx context.Context, slug string, document map[string]any, baseDigest, note string) (*ProposalReceipt, error) {
	receipt := &ProposalReceipt{}
	err := c.post(ctx, "/api/agent_context/proposals", map[string]any{
		"slug":        slug,
		"document":    document,
		"base_digest": baseDigest,
		"note":        note,
	}, receipt)
	if err != nil {
		return nil, err
	}
	return receipt, nil
}

// CreateSkillDraft mints a developer-initiated draft skill on the server —
// creator-only until published (capability: skill_drafts).
func (c *Client) CreateSkillDraft(ctx context.Context, name string) (*SkillDraft, error) {
	draft := &SkillDraft{}
	if err := c.post(ctx, "/api/agent_context/skills", map[string]any{"name": name}, draft); err != nil {
		return nil, err
	}
	return draft, nil
}

// ProjectContext fetches the project's estimation context bundle: the
// org-invariant prompt slice (rate-free), the project's complexity scale and
// releases, and the inventory of already-priced features an agent estimates
// against. Deliberately not part of the manifest — see the server's
// Api::ProjectContextsController for why one developer does not sync every
// project's priced backlog.
func (c *Client) ProjectContext(ctx context.Context, projectID int64) (*ProjectContext, error) {
	res, err := c.get(ctx, fmt.Sprintf("/api/agent_context/projects/%d/context", projectID), "")
	if err != nil {
		return nil, err
	}
	bundle := &ProjectContext{}
	if err := json.Unmarshal(res.Body, bundle); err != nil {
		return nil, fmt.Errorf("decode project context: %w", err)
	}
	return bundle, nil
}

// PushFeatures appends locally-produced features to a project's backlog.
//
// The server accepts the add action only, so this can never modify or
// destroy work already in the project. `action_name` rather than `action`
// because Rails reserves params[:action] for the controller action.
func (c *Client) PushFeatures(ctx context.Context, projectID int64, action string, features any) (*FeaturePushReceipt, error) {
	receipt := &FeaturePushReceipt{}
	err := c.post(ctx, fmt.Sprintf("/api/agent_context/projects/%d/features", projectID), map[string]any{
		"action_name": action,
		"features":    features,
	}, receipt)
	if err != nil {
		return nil, err
	}
	return receipt, nil
}

// PushArchitecture upserts a project's architecture profile.
func (c *Client) PushArchitecture(ctx context.Context, projectID int64, facts map[string]any, repository string) (*ArchitectureReceipt, error) {
	receipt := &ArchitectureReceipt{}
	err := c.post(ctx, "/api/agent_context/architecture", map[string]any{
		"project_id": projectID,
		"facts":      facts,
		"repository": repository,
		"source":     "local_scan",
	}, receipt)
	if err != nil {
		return nil, err
	}
	return receipt, nil
}

// InsecureBaseURL reports whether the base URL sends the token over
// plaintext HTTP to a non-local host — the one configuration worth a
// warning on every run.
func InsecureBaseURL(base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	return host != "localhost" && host != "127.0.0.1" && host != "::1"
}

// ToolDefinition is one tool exactly as the SERVER defines it. The client
// deliberately holds no opinion about names, descriptions or schemas: the
// registry lives in Rails so that adding a tool is a deploy rather than a
// release of this binary, and re-stating any of it here would put that back.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolCatalogue struct {
	Tools []ToolDefinition `json:"tools"`
}

// ToolResult is a tools/call response. IsError marks a failure the MODEL
// should read and can retry from, as opposed to a transport fault, which
// arrives as an *Error instead.
type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Text joins every text block, which is all this contract currently emits.
func (r *ToolResult) Text() string {
	parts := make([]string, 0, len(r.Content))
	for _, block := range r.Content {
		if block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// McpTools fetches the catalogue this token is permitted to call. The server
// filters it, so what comes back is already the callable set.
func (c *Client) McpTools(ctx context.Context) ([]ToolDefinition, error) {
	res, err := c.get(ctx, "/api/mcp/tools", "")
	if err != nil {
		return nil, err
	}
	var catalogue toolCatalogue
	if err := json.Unmarshal(res.Body, &catalogue); err != nil {
		return nil, fmt.Errorf("decode tool catalogue: %w", err)
	}
	return catalogue.Tools, nil
}

func (c *Client) McpCall(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error) {
	if arguments == nil {
		arguments = map[string]any{}
	}
	var result ToolResult
	if err := c.post(ctx, "/api/mcp/call", map[string]any{
		"name": name, "arguments": arguments,
	}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// TelemetryTurn is one turn of agent work as the telemetry endpoint takes it.
//
// The instants are the CLIENT'S ASSERTION and the server stamps its own
// receipt time beside them — the thing being measured is reporting on itself,
// so the two must stay separable. Token fields are omitted when zero rather
// than sent as 0, because "no cache read" and "we did not measure" are
// different claims.
type TelemetryTurn struct {
	TurnIndex           int    `json:"turn_index"`
	StartedAt           string `json:"started_at"`
	EndedAt             string `json:"ended_at"`
	InputTokens         int64  `json:"input_tokens,omitempty"`
	OutputTokens        int64  `json:"output_tokens,omitempty"`
	CacheCreationTokens int64  `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int64  `json:"cache_read_tokens,omitempty"`
	Model               string `json:"model,omitempty"`
}

// TelemetryReceipt is the card's totals AFTER the post, not just what this
// request added — `recorded` counts turns now on record, so a retry reports
// the same number rather than zero.
type TelemetryReceipt struct {
	FeatureID          int64            `json:"feature_id"`
	Recorded           int              `json:"recorded"`
	Episodes           int              `json:"episodes"`
	AgentActiveSeconds float64          `json:"agent_active_seconds"`
	Tokens             map[string]int64 `json:"tokens"`
}

// PostAgentTelemetry records turns against a card. Requires a token carrying
// the `time` scope, and nothing else.
func (c *Client) PostAgentTelemetry(
	ctx context.Context, projectID, featureID int64, sessionRef, role string, turns []TelemetryTurn,
) (*TelemetryReceipt, error) {
	payload := map[string]any{
		"project_id":  projectID,
		"feature_id":  featureID,
		"session_ref": sessionRef,
		"turns":       turns,
	}
	if role != "" {
		payload["role"] = role
	}
	var receipt TelemetryReceipt
	if err := c.post(ctx, "/api/agent_context/telemetry", payload, &receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func (c *Client) get(ctx context.Context, path, etag string) (*DocumentResult, error) {
	endpoint := strings.TrimSuffix(c.BaseURL, "/") + path
	if c.OrganizationID != "" {
		endpoint += "?organization_id=" + url.QueryEscape(c.OrganizationID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	return c.do(req, path)
}

func (c *Client) post(ctx context.Context, path string, payload map[string]any, into any) error {
	if c.OrganizationID != "" {
		payload["organization_id"] = c.OrganizationID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := strings.TrimSuffix(c.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.do(req, path)
	if err != nil {
		return err
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(res.Body, into); err != nil {
		return fmt.Errorf("decode %s response: %w", path, err)
	}
	return nil
}

func (c *Client) do(req *http.Request, path string) (*DocumentResult, error) {
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("User-Agent", fmt.Sprintf("fulcrum/%s (%s/%s)", c.version(), runtime.GOOS, runtime.GOARCH))
	req.Header.Set("X-Fulcrum-Client", "fulcrum/"+c.version())

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err // transport error: typed distinctly from *Error by construction
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	switch {
	case resp.StatusCode == http.StatusNotModified:
		return &DocumentResult{ETag: resp.Header.Get("ETag"), NotModified: true}, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return &DocumentResult{Body: body, ETag: resp.Header.Get("ETag")}, nil
	default:
		return nil, newError(req.Method, path, resp, body)
	}
}

func (c *Client) version() string {
	if c.Version == "" {
		return "0.0.0"
	}
	return c.Version
}

// newError preserves the server's body — the org-list and project-pointer
// 422s are self-serve documentation — plus the machine-readable code.
func newError(method, path string, resp *http.Response, body []byte) *Error {
	apiErr := &Error{
		Status:     resp.StatusCode,
		Method:     method,
		Path:       path,
		RetryAfter: resp.Header.Get("Retry-After"),
		Body:       string(body),
	}
	var parsed struct {
		Error         string         `json:"error"`
		Code          string         `json:"code"`
		Organizations []Organization `json:"organizations"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil {
		apiErr.Code = parsed.Code
		apiErr.ServerMessage = parsed.Error
		apiErr.Organizations = parsed.Organizations
	}
	return apiErr
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
