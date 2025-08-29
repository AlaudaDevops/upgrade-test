package operatorhub

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"knative.dev/pkg/logging"
)

func (o *Operator) InstallArtifactVersion(ctx context.Context, version string) (*unstructured.Unstructured, error) {
	log := logging.FromContext(ctx)
	log.Infow("installing artifact version", "version", version)

	artifact, err := o.GetResource(ctx, o.artifact, systemNamespace, artifactGVR)
	if err != nil {
		return nil, fmt.Errorf("failed to get artifact: %v", err)
	}

	log.Infow("creating artifact version", "name", artifact.GetName())
	av, err := o.createArtifactVersion(ctx, version, artifact)
	if err != nil {
		return nil, fmt.Errorf("failed to create artifact version: %v", err)
	}

	log.Infow("waiting for artifact version to be present", "name", av.GetName())
	av, err = o.waitArtifactVersionPresent(ctx, av.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to wait for artifact version: %v", err)
	}

	csv, found, _ := unstructured.NestedString(av.Object, "status", "version")
	if !found || csv == "" {
		return nil, fmt.Errorf("failed to get CSV version from artifact version %s: status.version field is empty or not found", av.GetName())
	}

	log.Infow("waiting for package manifest", "csv", csv)
	err = o.waitPackageManifest(ctx, csv)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure package manifest: %v", err)
	}

	log.Infow("artifact version installed successfully", "name", av.GetName())
	return av, nil
}

func (o *Operator) createArtifactVersion(ctx context.Context, version string, artifact *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	avName := fmt.Sprintf("%s.%s", artifact.GetName(), version)
	av, err := o.GetResource(ctx, avName, systemNamespace, artifactVersionGVR)
	if err == nil && av != nil {
		return av, nil
	}

	av = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "app.alauda.io/v1alpha1",
			"kind":       "ArtifactVersion",
			"metadata": map[string]interface{}{
				"name":      avName,
				"namespace": systemNamespace,
				"annotations": map[string]interface{}{
					"kubectl-artifact": "kubectl-artfact",
				},
				"labels": map[string]interface{}{
					"cpaas.io/artifact-version": artifact.GetName(),
					"cpaas.io/library":          "platform",
				},
			},
			"spec": map[string]interface{}{
				"present": true,
				"tag":     version,
			},
		},
	}

	av.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: artifact.GetAPIVersion(),
			Kind:       artifact.GetKind(),
			Name:       artifact.GetName(),
			UID:        artifact.GetUID(),
		},
	})

	return o.client.Resource(artifactVersionGVR).Namespace(systemNamespace).Create(ctx, av, metav1.CreateOptions{})
}

func (o *Operator) waitArtifactVersionPresent(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	lastResource := &unstructured.Unstructured{}
	err := wait.PollUntilContextTimeout(ctx, o.interval, o.timeout, true, func(ctx context.Context) (done bool, err error) {
		obj, err := o.client.Resource(artifactVersionGVR).Namespace(systemNamespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		status, _, _ := unstructured.NestedMap(obj.Object, "status")
		if phase, ok := status["phase"].(string); ok && phase == "Present" {
			lastResource = obj
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		return nil, err
	}
	return lastResource, nil
}

func (o *Operator) waitPackageManifest(ctx context.Context, csv string) error {
	return wait.PollUntilContextTimeout(ctx, o.interval, o.timeout, true, func(ctx context.Context) (done bool, err error) {
		pm, err := o.client.Resource(packageManifestGVR).Namespace(systemNamespace).Get(ctx, o.name, metav1.GetOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return false, err
		}

		if pm == nil {
			return false, nil
		}

		channels, _, _ := unstructured.NestedSlice(pm.Object, "status", "channels")
		for _, channel := range channels {
			channelMap, ok := channel.(map[string]interface{})
			if !ok {
				continue
			}

			entries, _, _ := unstructured.NestedSlice(channelMap, "entries")
			for _, entry := range entries {
				entryMap, ok := entry.(map[string]interface{})
				if !ok {
					continue
				}

				csvName, _, _ := unstructured.NestedString(entryMap, "name")
				if strings.Contains(csvName, csv) {
					return true, nil
				}
			}
		}

		return false, nil
	})
}
