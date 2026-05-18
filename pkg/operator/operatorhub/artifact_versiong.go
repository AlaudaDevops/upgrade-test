package operatorhub

import (
	"context"
	"fmt"

	"github.com/AlaudaDevops/upgrade-test/pkg/config"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"knative.dev/pkg/logging"
)

// InstallArtifactVersion is the stable entry point invoked by UpgradeOperator.
// It returns the CSV name (read from av.status.version inside the violet flow)
// so the caller does not have to re-extract it — a second extraction site was
// prone to silently passing an empty csv to InstallSubscription.
func (o *Operator) InstallArtifactVersion(ctx context.Context, version config.Version) (*unstructured.Unstructured, string, error) {
	if o.violet == nil {
		return nil, "", fmt.Errorf("operatorConfig.violet must be configured to install ArtifactVersion via the violet binary")
	}
	return o.installViaViolet(ctx, version)
}

// createArtifactVersion is no longer reachable from InstallArtifactVersion as
// of PR 2 — the violet binary owns the write path. Kept here as dead code for
// one release cycle so PR 2 regressions can be diff-compared against the
// previous in-process implementation. PR 3 deletes this function.
//
// Deprecated: use installViaViolet via InstallArtifactVersion.
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
	log := logging.FromContext(ctx)
	lastResource := &unstructured.Unstructured{}
	err := wait.PollUntilContextTimeout(ctx, o.interval, o.timeout, true, func(ctx context.Context) (done bool, err error) {
		obj, err := o.client.Resource(artifactVersionGVR).Namespace(systemNamespace).Get(ctx, name, metav1.GetOptions{})
		switch {
		case errors.IsNotFound(err):
			// AV not yet visible — eventual consistency window after violet push.
			return false, nil
		case isTransientAPIError(err):
			log.Warnw("transient API error while polling AV, will retry", "name", name, "err", err)
			return false, nil
		case err != nil:
			return false, fmt.Errorf("get AV %s: %w", name, err)
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
	log := logging.FromContext(ctx)
	return wait.PollUntilContextTimeout(ctx, o.interval, o.timeout, true, func(ctx context.Context) (done bool, err error) {
		pm, err := o.client.Resource(packageManifestGVR).Namespace(systemNamespace).Get(ctx, o.name, metav1.GetOptions{})
		switch {
		case errors.IsNotFound(err):
			return false, nil
		case isTransientAPIError(err):
			log.Warnw("transient API error while polling PackageManifest, will retry", "name", o.name, "err", err)
			return false, nil
		case err != nil:
			return false, fmt.Errorf("get PackageManifest %s: %w", o.name, err)
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

				csvName, found, err := unstructured.NestedString(entryMap, "name")
				if err != nil {
					return false, fmt.Errorf("PackageManifest %s entry .name has wrong type: %w", o.name, err)
				}
				if found && csvName == csv {
					return true, nil
				}
			}
		}

		return false, nil
	})
}

// isTransientAPIError reports whether err is a Kubernetes API error worth
// retrying on (apiserver timeout, throttling, brief unavailability). Permanent
// errors (RBAC denied, Forbidden, malformed request) must propagate so the
// poll loop fails fast with the real reason instead of timing out.
func isTransientAPIError(err error) bool {
	return errors.IsServerTimeout(err) ||
		errors.IsTooManyRequests(err) ||
		errors.IsServiceUnavailable(err)
}
