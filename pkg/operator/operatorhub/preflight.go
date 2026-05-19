package operatorhub

import (
	"context"

	"github.com/AlaudaDevops/upgrade-test/pkg/config"
	"github.com/AlaudaDevops/upgrade-test/pkg/operator/preflight"
)

// PreflightBaseline contract: this method MUST NOT mutate cluster state. It
// inspects Subscription / ArtifactVersion / non-terminal InstallPlan residue
// for the baseline `version` and reports findings as []preflight.Residual.
//
// This file is intentionally a thin scaffold; the real check logic lives in
// the follow-up commit so the interface plumbing can compile on its own.
func (o *Operator) PreflightBaseline(ctx context.Context, version config.Version) ([]preflight.Residual, error) {
	// Commit 1 scaffold: real implementation lands in Commit 2 (preflight checks).
	return nil, nil
}
