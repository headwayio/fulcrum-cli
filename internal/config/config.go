// Package config resolves where the client points and who it is. The env
// var names are the Ruby reference client's exact four — FULCRUM_URL,
// FULCRUM_API_TOKEN, FULCRUM_ORG_ID, FULCRUM_SKILLS_DIR — so every document
// stays singular. The token is never written to config.json or sync state:
// it lives in the env, the OS keyring, or a 0600 file, in that order.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

// Env var names, shared verbatim with the Ruby client.
const (
	EnvURL       = "FULCRUM_URL"
	EnvToken     = "FULCRUM_API_TOKEN"
	EnvOrgID     = "FULCRUM_ORG_ID"
	EnvSkillsDir = "FULCRUM_SKILLS_DIR"
)

// keyringService namespaces our secret in the OS keychain.
const keyringService = "fulcrum-cli"
const keyringUser = "api-token"

// tokenFileName is the fallback secret file inside the config dir.
const tokenFileName = "token"

// File is what config.json holds. Never a token.
type File struct {
	URL            string `json:"url"`
	OrganizationID string `json:"organization_id,omitempty"`
	SkillsDir      string `json:"skills_dir,omitempty"`
}

// Resolved is the effective configuration after env > file resolution.
type Resolved struct {
	URL            string
	Token          string
	OrganizationID string
	SkillsDir      string
	// TokenSource names where the token came from: "env", "keyring",
	// "file", or "" when absent — first-run detection keys on this.
	TokenSource string
}

// Keyring abstracts the OS keychain so tests can fake it and --no-keychain
// can bypass it.
type Keyring interface {
	Get() (string, error)
	Set(secret string) error
	Delete() error
}

// SystemKeyring is the real OS keychain.
type SystemKeyring struct{}

func (SystemKeyring) Get() (string, error) { return keyring.Get(keyringService, keyringUser) }
func (SystemKeyring) Set(secret string) error {
	return keyring.Set(keyringService, keyringUser, secret)
}
func (SystemKeyring) Delete() error { return keyring.Delete(keyringService, keyringUser) }

// Dir is the config directory (created on demand by writers).
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "fulcrum"), nil
}

// DefaultSkillsDir matches the Ruby client's default workspace.
func DefaultSkillsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".fulcrum/skills"
	}
	return filepath.Join(home, ".fulcrum", "skills")
}

// LoadFile reads config.json from dir; a missing file is an empty config.
func LoadFile(dir string) (*File, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if os.IsNotExist(err) {
		return &File{}, nil
	}
	if err != nil {
		return nil, err
	}
	f := &File{}
	if err := json.Unmarshal(raw, f); err != nil {
		return nil, fmt.Errorf("parse config.json: %w", err)
	}
	return f, nil
}

// SaveFile writes config.json (0600 — it names servers and orgs, not
// secrets, but there is no reason to share it).
func (f *File) SaveFile(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), append(raw, '\n'), 0o600)
}

// Resolve applies the chain: env beats file for URL/org/dir; the token chain
// is env → keyring → 0600 file. getenv is injectable for tests.
func Resolve(dir string, ring Keyring, getenv func(string) string) (*Resolved, error) {
	file, err := LoadFile(dir)
	if err != nil {
		return nil, err
	}

	resolved := &Resolved{
		URL:            firstNonEmpty(getenv(EnvURL), file.URL),
		OrganizationID: firstNonEmpty(getenv(EnvOrgID), file.OrganizationID),
		SkillsDir:      firstNonEmpty(getenv(EnvSkillsDir), file.SkillsDir, DefaultSkillsDir()),
	}

	if token := getenv(EnvToken); token != "" {
		resolved.Token, resolved.TokenSource = token, "env"
		return resolved, nil
	}
	if ring != nil {
		if token, err := ring.Get(); err == nil && token != "" {
			resolved.Token, resolved.TokenSource = token, "keyring"
			return resolved, nil
		}
	}
	if token, err := readTokenFile(dir); err != nil {
		return nil, err
	} else if token != "" {
		resolved.Token, resolved.TokenSource = token, "file"
	}
	return resolved, nil
}

// StoreToken prefers the keyring; when it is unavailable (headless Linux,
// --no-keychain) the token lands in a 0600 file instead. Returns where it
// went ("keyring" or "file").
func StoreToken(dir string, ring Keyring, token string) (string, error) {
	if ring != nil {
		if err := ring.Set(token); err == nil {
			// A stale file copy must not shadow future rotations.
			_ = os.Remove(filepath.Join(dir, tokenFileName))
			return "keyring", nil
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, tokenFileName), []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return "file", nil
}

// DeleteToken removes the secret from every store it might live in.
func DeleteToken(dir string, ring Keyring) error {
	var ringErr error
	if ring != nil {
		if err := ring.Delete(); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			ringErr = err
		}
	}
	if err := os.Remove(filepath.Join(dir, tokenFileName)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return ringErr
}

func readTokenFile(dir string) (string, error) {
	path := filepath.Join(dir, tokenFileName)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s is group/world readable (%o) — chmod 600 it", path, info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return trimNewlines(string(raw)), nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func trimNewlines(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
