package operatorhub

import (
	"context"
	"fmt"
	"time"

	"github.com/AlaudaDevops/upgrade-test/pkg/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// Operator represents the operator deployment manager
type Operator struct {
	client    dynamic.Interface
	namespace string
	name      string
	artifact  string

	timeout  time.Duration
	interval time.Duration
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
func NewOperator(config *rest.Config, options config.OperatorConfig) (*Operator, error) {
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	artifact := options.Artifact
	if artifact == "" {
		artifact = fmt.Sprintf("%s-%s", options.ArtifactPrefix, options.Name)
	}

	return &Operator{
		client:    client,
		namespace: options.Namespace,
		name:      options.Name,
		artifact:  artifact,
		timeout:   options.Timeout,
		interval:  options.Interval,
	}, nil
}

func (o *Operator) GetResource(ctx context.Context, name, namespace string, gvr schema.GroupVersionResource) (*unstructured.Unstructured, error) {
	return o.client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (o *Operator) UpgradeOperator(ctx context.Context, version config.Version) error {
	// Install artifact version
	av, err := o.InstallArtifactVersion(ctx, version.BundleVersion)
	if err != nil {
		return fmt.Errorf("failed to prepare operator: %v", err)
	}

	// Get CSV version from artifact version
	csv, _, _ := unstructured.NestedString(av.Object, "status", "version")
	channel := version.Channel
	if channel == "" {
		channel = "stable" // default fallback
	}
	if err := o.InstallSubscription(ctx, csv, channel); err != nil {
		return fmt.Errorf("failed to install subscription: %v", err)
	}

	return nil
}
