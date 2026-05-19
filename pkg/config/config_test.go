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
//
// The fixture sets namespace explicitly so the newer namespace-required check
// does not short-circuit before channel validation gets to run.
func TestLoadConfig_AppliesOperatorhubDefaultWhenTypeOmitted(t *testing.T) {
	yaml := `
operatorConfig:
  name: tektoncd-operator
  namespace: tektoncd-pipelines
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

// TestLoadConfig_RejectsMissingNamespaceForOperatorhub: preflight reads
// Subscription / InstallPlan from the configured namespace; an empty value
// would silently degrade to cluster-scope queries and produce false-positive
// residue reports. Catch at load time.
func TestLoadConfig_RejectsMissingNamespaceForOperatorhub(t *testing.T) {
	yaml := `
operatorConfig:
  type: operatorhub
  name: tektoncd-operator
upgradePaths:
  - name: main
    versions:
      - name: first
        bundleVersion: 4.0.17
        channel: stable
`
	_, err := LoadConfig(writeTempYAML(t, yaml))
	if err == nil {
		t.Fatal("operatorhub type without namespace must be rejected at load time")
	}
	if !strings.Contains(err.Error(), "namespace is required") {
		t.Errorf("error should explain the namespace requirement, got: %v", err)
	}
}

// TestLoadConfig_AllowsMissingNamespaceForLocal mirrors the channel-required
// gating: local mode runs `make deploy` and has no OLM namespace concept.
func TestLoadConfig_AllowsMissingNamespaceForLocal(t *testing.T) {
	yaml := `
operatorConfig:
  type: local
  name: tektoncd-operator
upgradePaths:
  - name: main
    versions:
      - name: first
        bundleVersion: 4.0.17
`
	if _, err := LoadConfig(writeTempYAML(t, yaml)); err != nil {
		t.Fatalf("local type should not require namespace: %v", err)
	}
}

// TestLoadConfig_RejectsShellMetaCharacterInBundleVersion: BundleVersion is
// interpolated into kubectl cleanup hints and violet argv; allowing $, `, or
// quotes here would propagate to user shells. The single chokepoint test
// guards every downstream consumer.
func TestLoadConfig_RejectsShellMetaCharacterInBundleVersion(t *testing.T) {
	// Single-quoted YAML scalars accept embedded double quotes verbatim, so
	// these test cases reach validateConfig instead of being rejected by the
	// YAML parser one layer earlier.
	cases := []string{`1.0$(whoami)`, "1.0;ls", "1.0 spaces", "1.0`backtick`", `1.0"quote`}
	for _, bv := range cases {
		t.Run(bv, func(t *testing.T) {
			yaml := `
operatorConfig:
  type: operatorhub
  name: tektoncd-operator
  namespace: tektoncd-pipelines
upgradePaths:
  - name: main
    versions:
      - name: first
        bundleVersion: '` + bv + `'
        channel: stable
`
			_, err := LoadConfig(writeTempYAML(t, yaml))
			if err == nil {
				t.Fatalf("bundleVersion %q must be rejected", bv)
			}
			if !strings.Contains(err.Error(), "bundleVersion") {
				t.Errorf("error should name the field, got: %v", err)
			}
		})
	}
}

// TestLoadConfig_AcceptsRealisticBundleVersions verifies the regex is not
// over-tight against versions observed in production configs.
func TestLoadConfig_AcceptsRealisticBundleVersions(t *testing.T) {
	cases := []string{"v4.6.3", "4.6.3-rc.91", "v0.74.0", "4.0.17"}
	for _, bv := range cases {
		t.Run(bv, func(t *testing.T) {
			yaml := `
operatorConfig:
  type: operatorhub
  name: tektoncd-operator
  namespace: tektoncd-pipelines
upgradePaths:
  - name: main
    versions:
      - name: first
        bundleVersion: ` + bv + `
        channel: stable
`
			if _, err := LoadConfig(writeTempYAML(t, yaml)); err != nil {
				t.Fatalf("bundleVersion %q should be accepted: %v", bv, err)
			}
		})
	}
}
