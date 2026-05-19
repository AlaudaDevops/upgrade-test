package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/AlaudaDevops/upgrade-test/pkg/config"
	"github.com/AlaudaDevops/upgrade-test/pkg/operator/preflight"
)

// fakeOperator implements operator.OperatorInterface for tests of runPreflight.
// Behavior is parameterised through residualsPerCall (one slice per
// PreflightBaseline call, in order) and preflightErr (returned from the first
// call regardless of residualsPerCall when non-nil).
//
// Counters are atomic so a future parallel test will not race; today's
// sequential callers wouldn't need it, but the cost is negligible.
type fakeOperator struct {
	residualsPerCall [][]preflight.Residual
	preflightErr     error
	preflightCalls   int32
	upgradeCalls     int32
	receivedVersions []config.Version
}

func (f *fakeOperator) UpgradeOperator(ctx context.Context, v config.Version) error {
	atomic.AddInt32(&f.upgradeCalls, 1)
	return nil
}

func (f *fakeOperator) PreflightBaseline(ctx context.Context, v config.Version) ([]preflight.Residual, error) {
	idx := atomic.AddInt32(&f.preflightCalls, 1) - 1
	f.receivedVersions = append(f.receivedVersions, v)
	if f.preflightErr != nil {
		return nil, f.preflightErr
	}
	if int(idx) >= len(f.residualsPerCall) {
		return nil, nil
	}
	return f.residualsPerCall[idx], nil
}

// writeKubeconfig drops a minimal valid kubeconfig YAML with the given
// current-context into t.TempDir() and returns its path. Passing an empty
// string yields a file with no current-context set — the "user forgot
// kubectl config use-context" scenario.
func writeKubeconfig(t *testing.T, currentContext string) string {
	t.Helper()
	body := `apiVersion: v1
kind: Config
clusters:
- name: devops
  cluster:
    server: https://example.invalid
contexts:
- name: devops
  context:
    cluster: devops
    user: admin
- name: prod
  context:
    cluster: devops
    user: admin
users:
- name: admin
  user: {}
current-context: ` + currentContext + "\n"
	path := filepath.Join(t.TempDir(), "kubeconfig.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}

func violetCfg(clusters string) *config.Config {
	cfg := &config.Config{}
	if clusters != "" {
		cfg.OperatorConfig.Violet = &config.VioletConfig{Clusters: clusters}
	}
	return cfg
}

// --- assertClusterMatch ----------------------------------------------------

func TestAssertClusterMatch_VioletNilIsNoOp(t *testing.T) {
	uc := &UpgradeCommand{}
	if err := uc.assertClusterMatch(&config.Config{}, "/any/path"); err != nil {
		t.Fatalf("nil violet should skip guard, got %v", err)
	}
}

func TestAssertClusterMatch_ClustersEmptyIsNoOp(t *testing.T) {
	uc := &UpgradeCommand{}
	cfg := &config.Config{}
	cfg.OperatorConfig.Violet = &config.VioletConfig{Clusters: ""}
	if err := uc.assertClusterMatch(cfg, "/any/path"); err != nil {
		t.Fatalf("empty clusters should skip guard, got %v", err)
	}
}

// In-cluster mode (kubeconfig path is empty): the guard cannot fall back to
// CurrentContext, so --confirm-cluster must equal violet.clusters. Missing
// flag is a hard error — this is the "CI pod silently writes to whatever
// serviceaccount points at" failure that #013 closes.
func TestAssertClusterMatch_InClusterRequiresConfirmFlag(t *testing.T) {
	uc := &UpgradeCommand{confirmCluster: ""}
	err := uc.assertClusterMatch(violetCfg("devops"), "")
	if err == nil {
		t.Fatal("in-cluster mode without --confirm-cluster must fail")
	}
	if !strings.Contains(err.Error(), "--confirm-cluster") {
		t.Errorf("error should mention --confirm-cluster: %v", err)
	}
}

func TestAssertClusterMatch_InClusterRejectsMismatchedConfirm(t *testing.T) {
	uc := &UpgradeCommand{confirmCluster: "global"}
	err := uc.assertClusterMatch(violetCfg("devops"), "")
	if err == nil {
		t.Fatal("--confirm-cluster != violet.clusters must fail in-cluster")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("expected 'does not match' in: %v", err)
	}
}

func TestAssertClusterMatch_InClusterHappyPath(t *testing.T) {
	uc := &UpgradeCommand{confirmCluster: "devops"}
	if err := uc.assertClusterMatch(violetCfg("devops"), ""); err != nil {
		t.Fatalf("matching --confirm-cluster should pass: %v", err)
	}
}

// File mode: --confirm-cluster must equal kubeconfig's current-context.
// An empty current-context is a user configuration error and the guard
// surfaces it actionably (#014).
func TestAssertClusterMatch_FileModeLoadFailureWraps(t *testing.T) {
	uc := &UpgradeCommand{}
	err := uc.assertClusterMatch(violetCfg("devops"), "/no/such/kubeconfig")
	if err == nil {
		t.Fatal("unreadable kubeconfig should fail the guard")
	}
	if !strings.Contains(err.Error(), "kubeconfig") {
		t.Errorf("error should name the file: %v", err)
	}
}

func TestAssertClusterMatch_FileModeEmptyCurrentContextFails(t *testing.T) {
	uc := &UpgradeCommand{}
	kc := writeKubeconfig(t, "")
	err := uc.assertClusterMatch(violetCfg("devops"), kc)
	if err == nil {
		t.Fatal("empty current-context must fail (was warn-and-pass before #014)")
	}
	if !strings.Contains(err.Error(), "use-context") {
		t.Errorf("error should hint at `kubectl config use-context`: %v", err)
	}
}

func TestAssertClusterMatch_FileModeRequiresConfirmFlag(t *testing.T) {
	uc := &UpgradeCommand{confirmCluster: ""}
	kc := writeKubeconfig(t, "devops")
	err := uc.assertClusterMatch(violetCfg("devops"), kc)
	if err == nil {
		t.Fatal("file mode without --confirm-cluster must fail")
	}
	if !strings.Contains(err.Error(), "--confirm-cluster") {
		t.Errorf("error should mention the flag: %v", err)
	}
}

func TestAssertClusterMatch_FileModeMismatchedConfirm(t *testing.T) {
	uc := &UpgradeCommand{confirmCluster: "prod"}
	kc := writeKubeconfig(t, "devops")
	err := uc.assertClusterMatch(violetCfg("devops"), kc)
	if err == nil {
		t.Fatal("mismatched --confirm-cluster vs current-context must fail")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("expected 'does not match' in: %v", err)
	}
}

func TestAssertClusterMatch_FileModeHappyPath(t *testing.T) {
	uc := &UpgradeCommand{confirmCluster: "devops"}
	kc := writeKubeconfig(t, "devops")
	if err := uc.assertClusterMatch(violetCfg("devops"), kc); err != nil {
		t.Fatalf("matching --confirm-cluster vs current-context should pass: %v", err)
	}
}

// --- runPreflight ----------------------------------------------------------

func newCmdWithPaths(op *fakeOperator, paths ...config.UpgradePath) *UpgradeCommand {
	return &UpgradeCommand{
		operator: op,
		config:   &config.Config{UpgradePaths: paths},
	}
}

func TestRunPreflight_AllCleanReturnsNil(t *testing.T) {
	op := &fakeOperator{}
	uc := newCmdWithPaths(op,
		config.UpgradePath{Name: "p1", Versions: []config.Version{{Name: "baseline", BundleVersion: "v1"}}},
	)
	if err := uc.runPreflight(context.Background()); err != nil {
		t.Fatalf("clean preflight should return nil: %v", err)
	}
	if op.preflightCalls != 1 {
		t.Errorf("expected 1 preflight call, got %d", op.preflightCalls)
	}
}

func TestRunPreflight_EmptyVersionsSkipped(t *testing.T) {
	op := &fakeOperator{}
	uc := newCmdWithPaths(op,
		config.UpgradePath{Name: "empty", Versions: nil},
	)
	if err := uc.runPreflight(context.Background()); err != nil {
		t.Fatalf("path with no versions should be skipped: %v", err)
	}
	if op.preflightCalls != 0 {
		t.Errorf("PreflightBaseline must not be called for empty Versions, got %d calls", op.preflightCalls)
	}
}

// Locks the design rule: PreflightBaseline only ever sees Versions[0],
// even when the path declares multiple downstream versions. A regression
// flipping to scan all versions would surface mid-upgrade AVs as false
// positives.
func TestRunPreflight_OnlyBaselineIsPassed(t *testing.T) {
	op := &fakeOperator{}
	uc := newCmdWithPaths(op, config.UpgradePath{
		Name: "multi-version",
		Versions: []config.Version{
			{Name: "baseline", BundleVersion: "v1"},
			{Name: "mid", BundleVersion: "v2"},
			{Name: "target", BundleVersion: "v3"},
		},
	})
	if err := uc.runPreflight(context.Background()); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if got := len(op.receivedVersions); got != 1 {
		t.Fatalf("expected 1 PreflightBaseline call, got %d", got)
	}
	if op.receivedVersions[0].BundleVersion != "v1" {
		t.Errorf("expected baseline (v1) only, got %q", op.receivedVersions[0].BundleVersion)
	}
}

// Multi-path fail-fast: once any baseline reports residuals, subsequent
// paths are not scanned. This avoids burning API calls when the first
// dirty path already proves the cluster needs cleanup.
func TestRunPreflight_FailFastAcrossPaths(t *testing.T) {
	op := &fakeOperator{
		residualsPerCall: [][]preflight.Residual{
			{preflight.NewResidual("Subscription", "ns", "foo")}, // path 1 dirty
			{}, // path 2 would be clean — must not be reached
		},
	}
	uc := newCmdWithPaths(op,
		config.UpgradePath{Name: "p1", Versions: []config.Version{{BundleVersion: "v1"}}},
		config.UpgradePath{Name: "p2", Versions: []config.Version{{BundleVersion: "v2"}}},
	)
	err := uc.runPreflight(context.Background())
	if err == nil {
		t.Fatal("expected PreflightError on dirty first path")
	}
	var pfe *PreflightError
	if !errors.As(err, &pfe) {
		t.Fatalf("expected *PreflightError, got %T: %v", err, err)
	}
	if len(pfe.Residuals) != 1 {
		t.Errorf("expected 1 residual, got %d", len(pfe.Residuals))
	}
	if op.preflightCalls != 1 {
		t.Errorf("second path's PreflightBaseline must not be called after first dirty, got %d calls", op.preflightCalls)
	}
}

func TestRunPreflight_OperatorErrorPropagatesUnwrapped(t *testing.T) {
	sentinel := errors.New("transient API timeout")
	op := &fakeOperator{preflightErr: sentinel}
	uc := newCmdWithPaths(op,
		config.UpgradePath{Name: "p1", Versions: []config.Version{{BundleVersion: "v1"}}},
	)
	err := uc.runPreflight(context.Background())
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error propagated, got %v", err)
	}
	// Make sure we did NOT wrap as PreflightError — operator errors are
	// distinct from "residue findings" and the cmd layer must not blur them.
	var pfe *PreflightError
	if errors.As(err, &pfe) {
		t.Errorf("operator errors must not be wrapped as *PreflightError")
	}
}
