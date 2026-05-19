package operator

import (
	"context"

	"github.com/AlaudaDevops/upgrade-test/pkg/config"
	"github.com/AlaudaDevops/upgrade-test/pkg/operator/preflight"
)

// OperatorInterface defines the interface for operator operations
type OperatorInterface interface {
	// UpgradeOperator upgrades the operator to the given version
	UpgradeOperator(ctx context.Context, version config.Version) error

	// PreflightBaseline inspects the cluster for residual resources that
	// would conflict with starting an upgrade chain at `version`. It is
	// strictly read-only: implementations MUST NOT mutate cluster state
	// (no Create/Update/Patch/Delete). Implementations are expected to
	// bound their own work with a short timeout (see operatorhub for the
	// 30s budget).
	//
	// Return values:
	//   - residuals: business-level findings that block the upgrade; the
	//     cmd layer aggregates and formats these into a user-facing report.
	//     An empty slice means the baseline is clean.
	//   - err: operational failures (network, transient API, permission).
	//     Callers should NOT mix err with residuals — err means "we could
	//     not finish checking", residuals means "we finished and found X".
	PreflightBaseline(ctx context.Context, version config.Version) ([]preflight.Residual, error)
}
