package operatorhub

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlaudaDevops/upgrade-test/pkg/config"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

// newPreflightOperator builds an Operator whose fake dynamic client knows
// about all four GVRs that preflight touches. Tests pass seed objects
// directly so each table case stays self-contained.
func newPreflightOperator(t *testing.T, seed ...runtime.Object) (*Operator, *fake.FakeDynamicClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(subscriptionGVR.GroupVersion().WithKind("Subscription"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(subscriptionGVR.GroupVersion().WithKind("SubscriptionList"), &unstructured.UnstructuredList{})
	scheme.AddKnownTypeWithName(installPlanGVR.GroupVersion().WithKind("InstallPlan"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(installPlanGVR.GroupVersion().WithKind("InstallPlanList"), &unstructured.UnstructuredList{})
	scheme.AddKnownTypeWithName(artifactVersionGVR.GroupVersion().WithKind("ArtifactVersion"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(artifactVersionGVR.GroupVersion().WithKind("ArtifactVersionList"), &unstructured.UnstructuredList{})

	client := fake.NewSimpleDynamicClient(scheme, seed...)
	op := &Operator{
		client:    client,
		namespace: "test-ns",
		name:      "tektoncd-operator",
		artifact:  "operatorhub-tektoncd-operator",
		interval:  10 * time.Millisecond,
		timeout:   2 * time.Second,
	}
	return op, client
}

// newAVUnstructured for preflight tests is shared with violet_test.go;
// both files live in the same package so a single definition is enough.
// (The spec fields it sets are read by violet tests but ignored here.)

// newIPWithPhase builds an InstallPlan tagged with the OLM-managed label that
// PreflightBaseline's ListOptions filter on. All test IPs carry the label
// because in production OLM emits them on every plan; tests that omit the
// label would be testing an impossible cluster state.
func newIPWithPhase(name, namespace, phase string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": installPlanGVR.GroupVersion().String(),
			"kind":       "InstallPlan",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"operators.coreos.com/tektoncd-operator.test-ns": "",
				},
			},
			"status": map[string]interface{}{
				"phase": phase,
			},
		},
	}
}

func TestPreflightBaseline_CleanCluster(t *testing.T) {
	op, _ := newPreflightOperator(t)
	res, err := op.PreflightBaseline(context.Background(), config.Version{BundleVersion: "v0.74.0"})
	if err != nil {
		t.Fatalf("clean cluster preflight: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("expected 0 residuals on clean cluster, got %d: %+v", len(res), res)
	}
}

func TestPreflightBaseline_SubscriptionResidue(t *testing.T) {
	sub := newSubscriptionUnstructured("tektoncd-operator", "test-ns", "stable")
	op, _ := newPreflightOperator(t, sub)

	res, err := op.PreflightBaseline(context.Background(), config.Version{BundleVersion: "v0.74.0"})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if len(res) != 1 || res[0].Kind != "Subscription" {
		t.Fatalf("expected 1 Subscription residual, got %+v", res)
	}
	if res[0].Name != "tektoncd-operator" || res[0].Namespace != "test-ns" {
		t.Errorf("residual identity wrong: %+v", res[0])
	}
	// cleanup hint MUST be safe to paste into a shell — names/ns quoted via %q.
	if !strings.Contains(res[0].RecommendedCleanup, `"tektoncd-operator"`) {
		t.Errorf("cleanup hint missing quoted name: %q", res[0].RecommendedCleanup)
	}
	if !strings.Contains(res[0].RecommendedCleanup, `"test-ns"`) {
		t.Errorf("cleanup hint missing quoted namespace: %q", res[0].RecommendedCleanup)
	}
}

func TestPreflightBaseline_ArtifactVersionResidue(t *testing.T) {
	av := newAVUnstructured("operatorhub-tektoncd-operator.v0.74.0")
	op, _ := newPreflightOperator(t, av)

	res, err := op.PreflightBaseline(context.Background(), config.Version{BundleVersion: "v0.74.0"})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if len(res) != 1 || res[0].Kind != "ArtifactVersion" {
		t.Fatalf("expected 1 AV residual, got %+v", res)
	}
	if res[0].Namespace != systemNamespace {
		t.Errorf("AV namespace must be %s, got %s", systemNamespace, res[0].Namespace)
	}
}

func TestPreflightBaseline_NonTerminalInstallPlanIsResidue(t *testing.T) {
	cases := []string{"", "Planning", "RequiresApproval", "Installing"}
	for _, phase := range cases {
		t.Run("phase="+phase, func(t *testing.T) {
			ip := newIPWithPhase("install-abc", "test-ns", phase)
			op, _ := newPreflightOperator(t, ip)

			res, err := op.PreflightBaseline(context.Background(), config.Version{BundleVersion: "v0.74.0"})
			if err != nil {
				t.Fatalf("preflight: %v", err)
			}
			if len(res) != 1 || res[0].Kind != "InstallPlan" {
				t.Fatalf("phase=%q should yield 1 IP residual, got %+v", phase, res)
			}
		})
	}
}

func TestPreflightBaseline_TerminalInstallPlanIgnored(t *testing.T) {
	for _, phase := range []string{"Complete", "Failed"} {
		t.Run("phase="+phase, func(t *testing.T) {
			ip := newIPWithPhase("install-old", "test-ns", phase)
			op, _ := newPreflightOperator(t, ip)

			res, err := op.PreflightBaseline(context.Background(), config.Version{BundleVersion: "v0.74.0"})
			if err != nil {
				t.Fatalf("preflight: %v", err)
			}
			if len(res) != 0 {
				t.Errorf("terminal phase %q must be ignored, got %+v", phase, res)
			}
		})
	}
}

func TestPreflightBaseline_AggregatesAcrossKinds(t *testing.T) {
	sub := newSubscriptionUnstructured("tektoncd-operator", "test-ns", "stable")
	av := newAVUnstructured("operatorhub-tektoncd-operator.v0.74.0")
	ip := newIPWithPhase("install-pending", "test-ns", "RequiresApproval")
	op, _ := newPreflightOperator(t, sub, av, ip)

	res, err := op.PreflightBaseline(context.Background(), config.Version{BundleVersion: "v0.74.0"})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	kinds := map[string]bool{}
	for _, r := range res {
		kinds[r.Kind] = true
	}
	for _, want := range []string{"Subscription", "ArtifactVersion", "InstallPlan"} {
		if !kinds[want] {
			t.Errorf("residual list missing kind %q; got %+v", want, res)
		}
	}
}

// TestPreflightBaseline_TransientErrorWrapsAsRetryHint ensures users see an
// actionable "retry the run" suffix on transient API failures, distinct from
// permanent failures that surface unmodified.
func TestPreflightBaseline_TransientErrorWrapsAsRetryHint(t *testing.T) {
	op, client := newPreflightOperator(t)
	client.PrependReactor("get", "subscriptions", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewServerTimeout(schema.GroupResource{Group: subscriptionGVR.Group, Resource: subscriptionGVR.Resource}, "get", 1)
	})

	_, err := op.PreflightBaseline(context.Background(), config.Version{BundleVersion: "v0.74.0"})
	if err == nil {
		t.Fatalf("expected error from transient API failure, got nil")
	}
	if !strings.Contains(err.Error(), "transient") {
		t.Errorf("transient hint missing from error: %v", err)
	}
}

// TestPreflightBaseline_PermanentErrorPropagates ensures a non-NotFound
// non-transient error bubbles up with the kind name so users can locate the
// failing check.
func TestPreflightBaseline_PermanentErrorPropagates(t *testing.T) {
	op, client := newPreflightOperator(t)
	wantErr := errors.New("forbidden by webhook")
	client.PrependReactor("get", "subscriptions", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, wantErr
	})

	_, err := op.PreflightBaseline(context.Background(), config.Version{BundleVersion: "v0.74.0"})
	if err == nil {
		t.Fatalf("expected permanent error, got nil")
	}
	if !strings.Contains(err.Error(), "Subscription") {
		t.Errorf("kind name missing from error: %v", err)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("wrap chain broken: %v", err)
	}
}

// TestPreflightBaseline_ReadOnly asserts the contract from interface.go:
// PreflightBaseline MUST NOT issue Create/Update/Patch/Delete against the
// dynamic client. A spy reactor fails the test if any mutation slips in.
func TestPreflightBaseline_ReadOnly(t *testing.T) {
	sub := newSubscriptionUnstructured("tektoncd-operator", "test-ns", "stable")
	av := newAVUnstructured("operatorhub-tektoncd-operator.v0.74.0")
	ip := newIPWithPhase("install-pending", "test-ns", "Installing")
	op, client := newPreflightOperator(t, sub, av, ip)

	var mutations int32
	for _, verb := range []string{"create", "update", "patch", "delete", "deletecollection"} {
		v := verb
		client.PrependReactor(v, "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
			atomic.AddInt32(&mutations, 1)
			t.Errorf("preflight must not %s; got action %+v", v, action)
			return false, nil, nil
		})
	}

	if _, err := op.PreflightBaseline(context.Background(), config.Version{BundleVersion: "v0.74.0"}); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if atomic.LoadInt32(&mutations) != 0 {
		t.Fatalf("preflight performed %d mutating actions, expected 0", mutations)
	}
}

// Confirm metav1.GetOptions is reachable here (import guard — if a future
// refactor drops the dependency by accident the test will catch it).
var _ = metav1.GetOptions{}
