package operator

import (
	"context"
	"time"

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
func NewOperator(config *rest.Config, namespace, name string) (*Operator, error) {
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &Operator{
		client:    client,
		namespace: namespace,
		name:      name,
		timeout:   10 * time.Minute,
		interval:  5 * time.Second,
	}, nil
}

func (o *Operator) GetResource(ctx context.Context, name, namespace string, gvr schema.GroupVersionResource) (*unstructured.Unstructured, error) {
	return o.client.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}
