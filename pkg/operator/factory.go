package operator

import (
	"k8s.io/client-go/rest"

	"github.com/AlaudaDevops/tools-upgrade-test/pkg/config"
	"github.com/AlaudaDevops/tools-upgrade-test/pkg/operator/local"
	"github.com/AlaudaDevops/tools-upgrade-test/pkg/operator/operatorhub"
)

// OperatorType represents the type of operator implementation
type OperatorType string

const (
	// OperatorTypeReal represents the real operator implementation
	OperatorTypeOperatorHub OperatorType = "operatorhub"
	OperatorTypeLocal       OperatorType = "local"
)

type OperatorOptions struct {
	// OperatorHub options
	Config    *rest.Config
	Namespace string
	Name      string

	// local deploy options
	OperatorConfig config.OperatorConfig
}

// OperatorFactory creates operator instances based on type
type OperatorFactory struct {
}

// NewOperatorFactory creates a new operator factory
func NewOperatorFactory() *OperatorFactory {
	return &OperatorFactory{}
}

// CreateOperator creates an operator instance based on the specified type
func (f *OperatorFactory) CreateOperator(operatorType OperatorType, options OperatorOptions) (OperatorInterface, error) {
	switch operatorType {
	case OperatorTypeLocal:
		return local.NewLocalOperator(options.OperatorConfig.Workspace, options.OperatorConfig.Command)
	default:
		return operatorhub.NewOperator(options.Config, options.Namespace, options.Name)
	}
}
