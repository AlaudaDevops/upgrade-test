package operatorhub

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

// newOperatorForSubscriptionTest builds a minimal *Operator whose dynamic
// client knows about Subscription / InstallPlan kinds. We use a single helper
// so the reactor wiring stays in one place and tests stay focused on the
// retry semantics, not on fake-client plumbing.
func newOperatorForSubscriptionTest(t *testing.T, seed ...runtime.Object) (*Operator, *fake.FakeDynamicClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(subscriptionGVR.GroupVersion().WithKind("Subscription"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(subscriptionGVR.GroupVersion().WithKind("SubscriptionList"), &unstructured.UnstructuredList{})
	scheme.AddKnownTypeWithName(installPlanGVR.GroupVersion().WithKind("InstallPlan"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(installPlanGVR.GroupVersion().WithKind("InstallPlanList"), &unstructured.UnstructuredList{})

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

func newSubscriptionUnstructured(name, namespace, channel string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": subscriptionGVR.GroupVersion().String(),
			"kind":       "Subscription",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"channel": channel,
			},
		},
	}
}

func newInstallPlanUnstructured(name, namespace string, approved bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": installPlanGVR.GroupVersion().String(),
			"kind":       "InstallPlan",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"approved": approved,
			},
		},
	}
}

// TestRefreshSubscriptionForUpgrade_RetriesOnConflict simulates the OLM
// controller updating the Subscription mid-flight: the first Update returns
// 409, the second succeeds. RetryOnConflict must keep the upgrade flow
// going instead of aborting the upgrade path.
func TestRefreshSubscriptionForUpgrade_RetriesOnConflict(t *testing.T) {
	sub := newSubscriptionUnstructured("tektoncd-operator", "test-ns", "stable")
	op, client := newOperatorForSubscriptionTest(t, sub)

	var updates int32
	client.PrependReactor("update", "subscriptions", func(action k8stesting.Action) (bool, runtime.Object, error) {
		// First Update → 409 Conflict. Second Update → fall through to the
		// real object tracker. RetryOnConflict applies backoff between
		// attempts, so this exercises both the retry loop and the fresh-Get
		// inside it.
		if atomic.AddInt32(&updates, 1) == 1 {
			return true, nil, errors.NewConflict(
				schema.GroupResource{Group: subscriptionGVR.Group, Resource: subscriptionGVR.Resource},
				"tektoncd-operator",
				nil,
			)
		}
		return false, nil, nil
	})

	if err := op.refreshSubscriptionForUpgrade(context.Background(), "stable-4.6"); err != nil {
		t.Fatalf("refresh should succeed after conflict retry: %v", err)
	}
	if got := atomic.LoadInt32(&updates); got < 2 {
		t.Errorf("expected at least 2 Update attempts after one 409, got %d", got)
	}

	// Confirm the final state has the new channel and a refresh annotation.
	got, err := client.Resource(subscriptionGVR).Namespace("test-ns").Get(context.Background(), "tektoncd-operator", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("final get: %v", err)
	}
	if ch, _, _ := unstructured.NestedString(got.Object, "spec", "channel"); ch != "stable-4.6" {
		t.Errorf("spec.channel = %q, want %q", ch, "stable-4.6")
	}
	if anno, _, _ := unstructured.NestedString(got.Object, "metadata", "annotations", refreshAnnotation); anno == "" {
		t.Errorf("refresh annotation missing on final object")
	}
}

// TestApproveInstallPlan_RetriesOnConflict mirrors the Subscription path: the
// IP's status is updated by OLM mid-flight and our spec.approved patch loses
// the version race once. The retry must re-Get and succeed without bubbling
// the 409 up to the upgrade path.
func TestApproveInstallPlan_RetriesOnConflict(t *testing.T) {
	ip := newInstallPlanUnstructured("install-abc123", "test-ns", false)
	op, client := newOperatorForSubscriptionTest(t, ip)

	var updates int32
	client.PrependReactor("update", "installplans", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if atomic.AddInt32(&updates, 1) == 1 {
			return true, nil, errors.NewConflict(
				schema.GroupResource{Group: installPlanGVR.Group, Resource: installPlanGVR.Resource},
				"install-abc123",
				nil,
			)
		}
		return false, nil, nil
	})

	if err := op.approveInstallPlan(context.Background(), "install-abc123", "test-ns"); err != nil {
		t.Fatalf("approve should succeed after conflict retry: %v", err)
	}
	if got := atomic.LoadInt32(&updates); got < 2 {
		t.Errorf("expected at least 2 Update attempts after one 409, got %d", got)
	}

	got, err := client.Resource(installPlanGVR).Namespace("test-ns").Get(context.Background(), "install-abc123", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("final get: %v", err)
	}
	if approved, _, _ := unstructured.NestedBool(got.Object, "spec", "approved"); !approved {
		t.Errorf("spec.approved = false, want true after retried approval")
	}
}

// TestApproveInstallPlan_AlreadyApprovedSkipsUpdate guards the idempotent
// short-circuit: when the IP is already approved we must not issue an Update,
// otherwise a re-run after a partial upgrade would generate avoidable API
// traffic (and racy 409s on a busy OLM).
func TestApproveInstallPlan_AlreadyApprovedSkipsUpdate(t *testing.T) {
	ip := newInstallPlanUnstructured("install-already-ok", "test-ns", true)
	op, client := newOperatorForSubscriptionTest(t, ip)

	var updates int32
	client.PrependReactor("update", "installplans", func(action k8stesting.Action) (bool, runtime.Object, error) {
		atomic.AddInt32(&updates, 1)
		return false, nil, nil
	})

	if err := op.approveInstallPlan(context.Background(), "install-already-ok", "test-ns"); err != nil {
		t.Fatalf("approve idempotent path: %v", err)
	}
	if got := atomic.LoadInt32(&updates); got != 0 {
		t.Errorf("Update must not be called for already-approved IP, got %d calls", got)
	}
}
