// pkg/operator/operator.go

package operator

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// Operator represents the operator deployment manager
type Operator struct {
	client dynamic.Interface
}

const (
	systemNamespace = "cpaas-system"
)

var (
	artifactGVR = schema.GroupVersionResource{
		Group:    "app.alauda.io",
		Version:  "v1alpha1",
		Resource: "artifacts",
	}
	artifactVersionGVR = schema.GroupVersionResource{
		Group:    "app.alauda.io",
		Version:  "v1alpha1",
		Resource: "artifactversions",
	}
	subscriptionGVR = schema.GroupVersionResource{
		Group:    "operators.coreos.com",
		Version:  "v1alpha1",
		Resource: "subscriptions",
	}

	namespaceGVR = schema.GroupVersionResource{
		Version:  "v1",
		Resource: "namespaces",
	}

	installPlanGVR = schema.GroupVersionResource{
		Group:    "operators.coreos.com",
		Version:  "v1alpha1",
		Resource: "installplans",
	}

	packageManifestGVR = schema.GroupVersionResource{
		Group:    "packages.operators.coreos.com",
		Version:  "v1",
		Resource: "packagemanifests",
	}

	csvGVR = schema.GroupVersionResource{
		Group:    "operators.coreos.com",
		Version:  "v1alpha1",
		Resource: "clusterserviceversions",
	}
)

// NewOperator creates a new Operator instance
func NewOperator(config *rest.Config) (*Operator, error) {
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &Operator{
		client: client,
	}, nil
}

// CreateArtifact creates or updates an Artifact resource if necessary.
// If the Artifact already exists, it checks whether the registry and repository match.
// If they match, the operation is skipped. If not, the resource is updated.
func (o *Operator) CreateArtifact(ctx context.Context, name, registry, repository string) (*unstructured.Unstructured, error) {
	// Define the desired Artifact object
	artifact := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "app.alauda.io/v1alpha1",
			"kind":       "Artifact",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": systemNamespace,
				"labels": map[string]interface{}{
					"cpaas.io/builtin": "false",
					"cpaas.io/library": "custom",
					"cpaas.io/present": "true",
					"cpaas.io/type":    "bundle",
				},
			},
			"spec": map[string]interface{}{
				"artifactVersionSelector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"cpaas.io/artifact-version": name,
					},
				},
				"description": "test operator bundle image",
				"displayName": "test operator",
				"present":     true,
				"registry":    registry,
				"repository":  repository,
				"type":        "bundle",
			},
		},
	}

	// Try to get the existing Artifact
	existing, err := o.client.Resource(artifactGVR).Namespace(systemNamespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil && existing != nil {
		// Artifact exists, check registry and repository
		spec, found, _ := unstructured.NestedMap(existing.Object, "spec")
		if found {
			existingRegistry, _ := spec["registry"].(string)
			existingRepository, _ := spec["repository"].(string)
			if existingRegistry == registry && existingRepository == repository {
				// Registry and repository match, skip update
				return existing, nil
			}
		}
		// Update the existing Artifact with new registry and repository
		if err := unstructured.SetNestedField(existing.Object, registry, "spec", "registry"); err != nil {
			return nil, fmt.Errorf("failed to set registry: %v", err)
		}
		if err := unstructured.SetNestedField(existing.Object, repository, "spec", "repository"); err != nil {
			return nil, fmt.Errorf("failed to set repository: %v", err)
		}
		existing, err = o.client.Resource(artifactGVR).Namespace(systemNamespace).Update(ctx, existing, metav1.UpdateOptions{})
		return existing, err
	}

	// If not found, create the Artifact
	artifact, err = o.client.Resource(artifactGVR).Namespace(systemNamespace).Create(ctx, artifact, metav1.CreateOptions{})
	return artifact, err
}

// CreateArtifactVersion creates an ArtifactVersion resource
// CreateArtifactVersion creates an ArtifactVersion resource. If the ArtifactVersion already exists, it will be deleted and recreated.
func (o *Operator) CreateArtifactVersion(ctx context.Context, name, tag string, artifact *unstructured.Unstructured) error {
	// Check if the ArtifactVersion already exists
	existing, err := o.client.Resource(artifactVersionGVR).Namespace(systemNamespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil && existing != nil {
		// If exists, delete it first
		if delErr := o.client.Resource(artifactVersionGVR).Namespace(systemNamespace).Delete(ctx, name, metav1.DeleteOptions{}); delErr != nil {
			return fmt.Errorf("failed to delete existing ArtifactVersion %s: %v", name, delErr)
		}
		// Wait for deletion to complete
		for {
			_, getErr := o.client.Resource(artifactVersionGVR).Namespace(systemNamespace).Get(ctx, name, metav1.GetOptions{})
			if getErr != nil {
				break // Not found, deleted
			}
			time.Sleep(1 * time.Second)
		}
	}

	artifactVersion := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "app.alauda.io/v1alpha1",
			"kind":       "ArtifactVersion",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": systemNamespace,
				"annotations": map[string]interface{}{
					"kubectl-artifact": "kubectl-artfact",
				},
				"labels": map[string]interface{}{
					"cpaas.io/artifact-version": artifact.GetName(),
					"cpaas.io/library":          "custom",
				},
			},
			"spec": map[string]interface{}{
				"present": true,
				"tag":     tag,
			},
		},
	}

	artifactVersion.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: artifact.GetAPIVersion(),
			Kind:       artifact.GetKind(),
			Name:       artifact.GetName(),
			UID:        artifact.GetUID(),
		},
	})

	artifactVersion, err = o.client.Resource(artifactVersionGVR).Namespace(systemNamespace).Create(ctx, artifactVersion, metav1.CreateOptions{})
	return err
}

// WaitForArtifactVersionPresent waits for the ArtifactVersion to be present
func (o *Operator) WaitForArtifactVersionPresent(ctx context.Context, name string, timeout time.Duration) (string, string, error) {
	gvr := schema.GroupVersionResource{
		Group:    "app.alauda.io",
		Version:  "v1alpha1",
		Resource: "artifactversions",
	}

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		obj, err := o.client.Resource(gvr).Namespace(systemNamespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", "", err
		}

		status, _, _ := unstructured.NestedMap(obj.Object, "status")
		if phase, ok := status["phase"].(string); ok && phase == "Present" {
			operatorName, hasOperatorName := status["name"].(string)
			startCSVName, hasStartCSVName := status["version"].(string)
			if hasOperatorName && hasStartCSVName {
				return operatorName, startCSVName, nil
			}
			return "", "", fmt.Errorf("operator name is not set for ArtifactVersion %s", name)
		}

		time.Sleep(5 * time.Second)
	}

	return "", "", fmt.Errorf("timeout waiting for ArtifactVersion %s to be present", name)
}
