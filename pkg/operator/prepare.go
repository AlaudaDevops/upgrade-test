package operator

import (
	"context"
	"fmt"
	"time"

	"github.com/AlaudaDevops/tools-upgrade-test/pkg/config"
	upctx "github.com/AlaudaDevops/tools-upgrade-test/pkg/context"
	oras "oras.land/oras-go/v2/registry"
)

// PrepareOperator prepares the operator for the upgrade test
func (o *Operator) PrepareOperator(ctx context.Context, artifactName string, version config.Version) (operatorName, artifactVersionName string, err error) {
	logger := upctx.LoggerFromContext(ctx)
	logger.Infow("starting operator preparation",
		"operatorName", artifactName,
		"version", version.Name,
		"image", version.BundleImage)

	if version.BundleImage == "" {
		return "", "", fmt.Errorf("bundle image is not set")
	}

	ref, err := oras.ParseReference(version.BundleImage)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse image reference: %v", err)
	}

	artifact, err := o.CreateArtifact(ctx, artifactName, ref.Registry, ref.Repository)
	if err != nil {
		return "", "", fmt.Errorf("failed to create artifact: %v", err)
	}
	logger.Infow("artifact created successfully",
		"name", artifactName)

	// Create ArtifactVersion
	name := fmt.Sprintf("%s.%s", artifactName, ref.Reference)
	if err := o.CreateArtifactVersion(ctx, name, ref.Reference, artifact); err != nil {
		return "", "", fmt.Errorf("failed to create artifact version: %v", err)
	}

	operatorName, startCSVName, err := o.WaitForArtifactVersionPresent(ctx, name, 10*time.Minute)
	if err != nil {
		return "", "", fmt.Errorf("failed to wait for artifact version: %v", err)
	}

	logger.Infow("operator prepared successfully",
		"operatorName", operatorName,
		"version", startCSVName)
	return operatorName, startCSVName, nil
}
