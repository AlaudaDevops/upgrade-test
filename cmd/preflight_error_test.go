package cmd

import (
	"strings"
	"testing"

	"github.com/AlaudaDevops/upgrade-test/pkg/operator/preflight"
)

func TestPreflightError_FormatsAllResiduals(t *testing.T) {
	err := &PreflightError{Residuals: []preflight.Residual{
		{Kind: "Subscription", Namespace: "test-ns", Name: "tektoncd-operator",
			RecommendedCleanup: `kubectl delete subscription "tektoncd-operator" -n "test-ns"`},
		{Kind: "ArtifactVersion", Namespace: "cpaas-system", Name: "operatorhub-tektoncd-operator.v0.74.0",
			RecommendedCleanup: `kubectl delete artifactversion "operatorhub-tektoncd-operator.v0.74.0" -n "cpaas-system"`},
	}}
	msg := err.Error()
	for _, want := range []string{
		"preflight failed: 2 residual resource(s)",
		"Subscription/tektoncd-operator",
		"ArtifactVersion/operatorhub-tektoncd-operator.v0.74.0",
		`kubectl delete subscription "tektoncd-operator"`,
		"finalizer stuck",
		"wait ~30s for OLM to settle",
		"--skip-preflight",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q\nfull message:\n%s", want, msg)
		}
	}
}

func TestPreflightError_ZeroResidualsStillReadable(t *testing.T) {
	// Defensive: if callers construct a PreflightError with no residuals, the
	// Error() output should still parse as a coherent message rather than
	// dangling templates. This case is not expected at runtime (cmd-layer
	// callers check len > 0 first) but the formatter must not crash.
	err := &PreflightError{}
	msg := err.Error()
	if !strings.Contains(msg, "0 residual") {
		t.Errorf("empty residual list should still report count; got: %s", msg)
	}
}
