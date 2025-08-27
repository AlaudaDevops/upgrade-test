package local

import (
	"context"
	"fmt"

	"github.com/AlaudaDevops/tools-upgrade-test/pkg/exec"
	"knative.dev/pkg/logging"
)

type LocalOperator struct {
	workDir         string
	command         string
	versionRevision map[string]string
}

func NewLocalOperator(workDir string, command string) (*LocalOperator, error) {
	return &LocalOperator{
		workDir: workDir,
		command: command,
	}, nil
}

func (o *LocalOperator) UpgradeOperator(ctx context.Context, version string) error {
	log := logging.FromContext(ctx)

	if o.command == "" {
		o.command = "make deploy"
	}

	log.Infof("upgrading operator version: %s", version)

	if err := o.runDeployCommand(ctx, version); err != nil {
		return fmt.Errorf("failed to run deploy command: %v", err)
	}

	log.Infof("operator version %s upgraded successfully", version)
	return nil
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
