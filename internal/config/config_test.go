package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeKeyring struct {
	secret string
	broken bool
}

var errFakeKeyring = errors.New("keyring unavailable")

func (f *fakeKeyring) Get() (string, error) {
	if f.broken || f.secret == "" {
		return "", errFakeKeyring
	}
	return f.secret, nil
}

func (f *fakeKeyring) Set(secret string) error {
	if f.broken {
		return errFakeKeyring
	}
	f.secret = secret
	return nil
}

func (f *fakeKeyring) Delete() error {
	f.secret = ""
	return nil
}

func env(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

func TestTokenChainEnvBeatsEverything(t *testing.T) {
	dir := t.TempDir()
	ring := &fakeKeyring{secret: "from-keyring"}
	if _, err := StoreToken(dir, nil, "from-file"); err != nil {
		t.Fatal(err)
	}

	resolved, err := Resolve(dir, ring, env(map[string]string{EnvToken: "from-env"}))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Token != "from-env" || resolved.TokenSource != "env" {
		t.Errorf("token = %q from %q", resolved.Token, resolved.TokenSource)
	}
}

func TestTokenChainKeyringBeatsFile(t *testing.T) {
	dir := t.TempDir()
	ring := &fakeKeyring{secret: "from-keyring"}
	if _, err := StoreToken(dir, nil, "from-file"); err != nil {
		t.Fatal(err)
	}

	resolved, err := Resolve(dir, ring, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Token != "from-keyring" || resolved.TokenSource != "keyring" {
		t.Errorf("token = %q from %q", resolved.Token, resolved.TokenSource)
	}
}

func TestTokenChainFileFallback(t *testing.T) {
	dir := t.TempDir()
	where, err := StoreToken(dir, &fakeKeyring{broken: true}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if where != "file" {
		t.Fatalf("broken keyring must fall back to file, got %q", where)
	}

	resolved, err := Resolve(dir, &fakeKeyring{broken: true}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Token != "tok" || resolved.TokenSource != "file" {
		t.Errorf("token = %q from %q", resolved.Token, resolved.TokenSource)
	}
}

func TestNoTokenAnywhere(t *testing.T) {
	resolved, err := Resolve(t.TempDir(), &fakeKeyring{}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Token != "" || resolved.TokenSource != "" {
		t.Errorf("expected absent token, got %q from %q", resolved.Token, resolved.TokenSource)
	}
}

func TestWorldReadableTokenFileRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, tokenFileName), []byte("tok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(dir, nil, env(nil)); err == nil {
		t.Error("a 0644 token file must be refused, not silently used")
	}
}

func TestStoreTokenKeyringRemovesStaleFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := StoreToken(dir, nil, "old-tok"); err != nil {
		t.Fatal(err)
	}
	ring := &fakeKeyring{}
	where, err := StoreToken(dir, ring, "new-tok")
	if err != nil || where != "keyring" {
		t.Fatalf("where=%q err=%v", where, err)
	}
	if _, err := os.Stat(filepath.Join(dir, tokenFileName)); !os.IsNotExist(err) {
		t.Error("stale token file must not shadow future keyring rotations")
	}
}

func TestConfigFileRoundTripAndEnvOverride(t *testing.T) {
	dir := t.TempDir()
	f := &File{URL: "https://usefulcrum.ai", OrganizationID: "9", SkillsDir: "/tmp/skills"}
	if err := f.SaveFile(dir); err != nil {
		t.Fatal(err)
	}

	// Token must never land in config.json.
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || filepath.Ext("config.json") != ".json" {
		t.Fatal("sanity")
	}
	for _, forbidden := range []string{"token", "secret"} {
		if containsFold(string(raw), forbidden) {
			t.Errorf("config.json must never mention %q:\n%s", forbidden, raw)
		}
	}

	resolved, err := Resolve(dir, nil, env(map[string]string{EnvURL: "http://localhost:3100"}))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.URL != "http://localhost:3100" {
		t.Errorf("env must beat file, got %q", resolved.URL)
	}
	if resolved.OrganizationID != "9" || resolved.SkillsDir != "/tmp/skills" {
		t.Errorf("file values lost: %+v", resolved)
	}
}

func TestSkillsDirDefault(t *testing.T) {
	resolved, err := Resolve(t.TempDir(), nil, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SkillsDir != DefaultSkillsDir() {
		t.Errorf("skills dir = %q, want default %q", resolved.SkillsDir, DefaultSkillsDir())
	}
	if filepath.Base(filepath.Dir(DefaultSkillsDir())) != ".fulcrum" {
		t.Errorf("default must match the Ruby client's ~/.fulcrum/skills, got %q", DefaultSkillsDir())
	}
}

func containsFold(haystack, needle string) bool {
	h, n := []rune(haystack), []rune(needle)
	for i := 0; i+len(n) <= len(h); i++ {
		match := true
		for j := range n {
			a, b := h[i+j], n[j]
			if 'A' <= a && a <= 'Z' {
				a += 'a' - 'A'
			}
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
