package operator

import (
	"context"

	"github.com/AlaudaDevops/upgrade-test/pkg/config"
)

// OperatorInterface defines the interface for operator operations
type OperatorInterface interface {
	// UpgradeOperator upgrades the operator to the given version
	UpgradeOperator(ctx context.Context, version config.Version) error
}
