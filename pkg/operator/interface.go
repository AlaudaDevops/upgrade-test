package operator

import "context"

// OperatorInterface defines the interface for operator operations
type OperatorInterface interface {
	// UpgradeOperator upgrades the operator to the given version
	UpgradeOperator(ctx context.Context, version string) error
}
