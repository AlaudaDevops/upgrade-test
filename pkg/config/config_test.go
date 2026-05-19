package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempYAML drops content into a temp file and returns its path. Kept
// inline (not a shared helper) because each table case is small and a helper
// would obscure what each YAML actually exercises.
func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "upgrade.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// TestLoadConfig_RejectsMissingChannelForOperatorhub locks the §1.3 fix in:
// the upgrade flow used to discover an empty channel mid-run (path N, version
// M), wasting all the prior steps. After defaults run, validateConfig must
// refuse to return a Config that would crash later.
func TestLoadConfig_RejectsMissingChannelForOperatorhub(t *testing.T) {
	yaml := `
operatorConfig:
  type: operatorhub
  name: tektoncd-operator
  namespace: tektoncd-pipelines
upgradePaths:
  - name: main
    versions:
      - name: first
        bundleVersion: 4.0.17
        channel: stable
      - name: second
        bundleVersion: 4.6.3
        # channel deliberately omitted
`
	_, err := LoadConfig(writeTempYAML(t, yaml))
	if err == nil {
		t.Fatal("LoadConfig must reject operatorhub config with missing channel")
	}
	// The error should point at the offending version so a real ops user can
	// find the line in their YAML quickly — index + name are both useful.
	msg := err.Error()
	if !strings.Contains(msg, "channel is required") || !strings.Contains(msg, "second") {
		t.Errorf("error should name the missing field and the failing version, got: %v", err)
	}
}

// TestLoadConfig_AllowsMissingChannelForLocal documents the boundary of the
// rule: the local operator type runs `make deploy` and never touches Channel,
// so forcing it on local-dev configs would break valid setups.
func TestLoadConfig_AllowsMissingChannelForLocal(t *testing.T) {
	yaml := `
operatorConfig:
  type: local
  name: tektoncd-operator
upgradePaths:
  - name: main
    versions:
      - name: first
        bundleVersion: 4.0.17
        # local flow does not need a channel
`
	cfg, err := LoadConfig(writeTempYAML(t, yaml))
	if err != nil {
		t.Fatalf("local type with empty channel should load: %v", err)
	}
	if cfg.OperatorConfig.Type != "local" {
		t.Errorf("Type = %q, want %q", cfg.OperatorConfig.Type, "local")
	}
}

// TestLoadConfig_AppliesOperatorhubDefaultWhenTypeOmitted catches a subtle
// failure mode: if validateConfig ran *before* defaultConfig, a config with
// no explicit type would skip channel validation, then crash at runtime when
// operatorhub kicked in. This test pins the ordering down.
func TestLoadConfig_AppliesOperatorhubDefaultWhenTypeOmitted(t *testing.T) {
	yaml := `
operatorConfig:
  name: tektoncd-operator
upgradePaths:
  - name: main
    versions:
      - name: first
        bundleVersion: 4.0.17
        # missing channel — should be caught because type defaults to operatorhub
`
	_, err := LoadConfig(writeTempYAML(t, yaml))
	if err == nil {
		t.Fatal("missing channel under defaulted operatorhub type must fail")
	}
	if !strings.Contains(err.Error(), "channel is required") {
		t.Errorf("expected channel-required error, got: %v", err)
	}
}

// TestLoadConfig_ValidOperatorhubLoadsCleanly is the positive-case smoke test
// so future tightening of validateConfig does not accidentally break the
// happy path.
func TestLoadConfig_ValidOperatorhubLoadsCleanly(t *testing.T) {
	yaml := `
operatorConfig:
  type: operatorhub
  name: tektoncd-operator
  namespace: tektoncd-pipelines
upgradePaths:
  - name: main
    versions:
      - name: first
        bundleVersion: 4.0.17
        channel: stable
      - name: second
        bundleVersion: 4.6.3
        channel: stable
`
	cfg, err := LoadConfig(writeTempYAML(t, yaml))
	if err != nil {
		t.Fatalf("valid config should load: %v", err)
	}
	if got := len(cfg.UpgradePaths[0].Versions); got != 2 {
		t.Errorf("Versions length = %d, want 2", got)
	}
}
