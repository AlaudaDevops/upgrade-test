package local

import (
	"context"
	"fmt"

	"github.com/AlaudaDevops/upgrade-test/pkg/config"
	"github.com/AlaudaDevops/upgrade-test/pkg/exec"
	"github.com/AlaudaDevops/upgrade-test/pkg/operator/preflight"
	"knative.dev/pkg/logging"
)

type LocalOperator struct {
	workDir         string
	command         string
	versionRevision map[string]string
}

func NewLocalOperator(options config.OperatorConfig) (*LocalOperator, error) {
	return &LocalOperator{
		workDir: options.Workspace,
		command: options.Command,
	}, nil
}

func (o *LocalOperator) UpgradeOperator(ctx context.Context, version config.Version) error {
	log := logging.FromContext(ctx)

	if o.command == "" {
		o.command = "make deploy"
	}

	log.Infof("upgrading operator version: %s", version.Name)

	if err := o.runDeployCommand(ctx, version.BundleVersion); err != nil {
		return fmt.Errorf("failed to run deploy command: %v", err)
	}

	log.Infof("operator version %s upgraded successfully", version.Name)
	return nil
}

// PreflightBaseline is a no-op for the local operator: there is no OLM
// machinery to inspect for residue, and `make deploy` is expected to be
// idempotent (documented in README). Returns (nil, nil) deliberately —
// "no findings, no errors" — so that the cmd layer treats local runs as
// always-clean and proceeds straight to UpgradeOperator.
func (o *LocalOperator) PreflightBaseline(ctx context.Context, version config.Version) ([]preflight.Residual, error) {
	return nil, nil
}

func (o *LocalOperator) runDeployCommand(ctx context.Context, version string) error {
	result := exec.RunCommand(ctx, exec.Command{
		Name: "bash",
		Args: []string{"-c", o.command},
		Dir:  o.workDir,
	})
	if result.Err != nil {
		return fmt.Errorf("failed to run deploy command: %v", result.Err)
	}
	return nil
}
