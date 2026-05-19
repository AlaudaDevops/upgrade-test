package operatorhub

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"knative.dev/pkg/logging"
)

// InstallSubscription drives an OLM in-place upgrade for one target CSV.
//
// First version on a fresh cluster: Subscription does not yet exist → create
// it (installPlanApproval: Manual, startingCSV: csv).
//
// Subsequent versions: Subscription already exists from the previous step.
// The new bundle has just been pushed to the platform catalog by violet, so
// OLM will produce a fresh InstallPlan whose spec.clusterServiceVersionNames
// targets the new csv. We do NOT delete the Subscription or the prior CSV —
// OLM's replace chain rolls them forward, and tearing them down here loses
// the upgrade-path semantics we want to exercise. We only:
//
//  1. patch spec.channel when the target channel differs from the current one
//     (no-op when it matches);
//  2. wait for the InstallPlan whose spec targets the new csv;
//  3. approve it (idempotent — already-approved plans are left alone);
//  4. wait for the new CSV phase=Succeeded.
func (o *Operator) InstallSubscription(ctx context.Context, csv string, channel string) error {
	if csv == "" {
		return fmt.Errorf("csv is empty")
	}

	log := logging.FromContext(ctx)
	log.Infow("ensuring subscription for upgrade", "csv", csv, "channel", channel, "namespace", o.namespace)

	_, err := o.client.Resource(subscriptionGVR).Namespace(o.namespace).Get(ctx, o.name, metav1.GetOptions{})
	switch {
	case errors.IsNotFound(err):
		log.Infow("subscription absent, creating fresh", "name", o.name, "csv", csv, "channel", channel)
		if _, err := o.createSubscription(ctx, o.name, o.namespace, csv, channel); err != nil {
			return fmt.Errorf("failed to create subscription: %v", err)
		}
	case err != nil:
		return fmt.Errorf("failed to get subscription: %v", err)
	default:
		log.Infow("subscription exists, rolling forward in place", "name", o.name)
		if err := o.refreshSubscriptionForUpgrade(ctx, channel); err != nil {
			return fmt.Errorf("failed to refresh subscription: %v", err)
		}
	}

	log.Infow("waiting for install plan targeting csv", "csv", csv)
	installPlanName, err := o.waitInstallPlanForCSV(ctx, o.name, o.namespace, csv)
	if err != nil {
		return fmt.Errorf("failed to wait for install plan: %v", err)
	}

	log.Infow("approving install plan", "installPlan", installPlanName, "csv", csv)
	if err := o.approveInstallPlan(ctx, installPlanName, o.namespace); err != nil {
		return fmt.Errorf("failed to approve install plan: %v", err)
	}

	log.Infow("waiting for csv to be ready", "name", csv, "namespace", o.namespace)
	if err := o.waitCSVReady(ctx, csv, o.namespace); err != nil {
		return fmt.Errorf("failed to wait for csv to be ready: %v", err)
	}

	log.Infow("subscription rolled forward", "name", o.name, "csv", csv)
	return nil
}

// refreshAnnotation is bumped on every in-place upgrade to nudge the OLM
// Subscription controller to re-reconcile, even when spec.channel does not
// change. The annotation lives under our own domain so it can never collide
// with OLM's internal olm.* keys.
const refreshAnnotation = "upgrade-test.alauda.io/refresh-trigger"

// refreshSubscriptionForUpgrade prepares an already-existing Subscription for
// the next upgrade step. It does two things in a single Update:
//
//  1. If the target channel differs from spec.channel, patch it.
//  2. Always bump refreshAnnotation to a fresh RFC3339Nano timestamp. This is
//     a force-refresh nudge: even on same-channel upgrades, mutating the
//     object's metadata makes the OLM controller re-evaluate the Subscription
//     against the catalog instead of waiting on its internal resync interval.
//     Useful when the new CSV has just been pushed by violet and the
//     PackageManifest cache is stale.
//
// We deliberately do NOT delete and recreate the Subscription, restart the
// catalog-operator, or touch the CatalogSource — annotation bump is the
// lightest mechanism that still guarantees a reconcile.
//
// Get is performed inside RetryOnConflict so that a 409 (OLM controller
// updated status concurrently) re-reads the latest resourceVersion before
// retrying the mutation. Without this, a busy OLM made the first upgrade
// step flaky on tektoncd-operator and similar high-traffic Subscriptions.
func (o *Operator) refreshSubscriptionForUpgrade(ctx context.Context, channel string) error {
	log := logging.FromContext(ctx)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		sub, err := o.client.Resource(subscriptionGVR).Namespace(o.namespace).Get(ctx, o.name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get subscription: %w", err)
		}

		current, _, _ := unstructured.NestedString(sub.Object, "spec", "channel")
		if current != channel {
			log.Infow("patching subscription channel", "from", current, "to", channel)
			if err := unstructured.SetNestedField(sub.Object, channel, "spec", "channel"); err != nil {
				return fmt.Errorf("set spec.channel: %w", err)
			}
		} else {
			log.Infow("subscription channel already matches target", "channel", channel)
		}

		annotations, _, _ := unstructured.NestedStringMap(sub.Object, "metadata", "annotations")
		if annotations == nil {
			annotations = map[string]string{}
		}
		stamp := time.Now().UTC().Format(time.RFC3339Nano)
		annotations[refreshAnnotation] = stamp
		if err := unstructured.SetNestedStringMap(sub.Object, annotations, "metadata", "annotations"); err != nil {
			return fmt.Errorf("set refresh annotation: %w", err)
		}
		log.Infow("force-refresh annotation bumped to trigger OLM reconcile", "annotation", refreshAnnotation, "value", stamp)

		_, err = o.client.Resource(subscriptionGVR).Namespace(o.namespace).Update(ctx, sub, metav1.UpdateOptions{})
		return err
	})
}

// approveInstallPlan flips spec.approved=true. Idempotent: if the plan is
// already approved (e.g. retry after a prior partial run), it returns nil
// without issuing an Update.
//
// Get + Update run inside RetryOnConflict so a concurrent OLM status write
// does not turn the approval into a hard failure — the upgrade flow has no
// pre-approved state to roll back to and a single 409 here would otherwise
// abort the entire upgrade path.
func (o *Operator) approveInstallPlan(ctx context.Context, name, namespace string) error {
	log := logging.FromContext(ctx)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		ip, err := o.client.Resource(installPlanGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get install plan: %w", err)
		}
		if approved, _, _ := unstructured.NestedBool(ip.Object, "spec", "approved"); approved {
			log.Infow("install plan already approved, skipping update", "name", name)
			return nil
		}
		if err := unstructured.SetNestedField(ip.Object, true, "spec", "approved"); err != nil {
			return fmt.Errorf("set spec.approved: %w", err)
		}
		_, err = o.client.Resource(installPlanGVR).Namespace(namespace).Update(ctx, ip, metav1.UpdateOptions{})
		return err
	})
}

func (o *Operator) createSubscription(ctx context.Context, name, namespace, csv string, channel string) (*unstructured.Unstructured, error) {
	log := logging.FromContext(ctx)

	_, err := o.client.Resource(namespaceGVR).Create(ctx, &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name": namespace,
			},
		},
	}, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("failed to create namespace: %v", err)
	}

	subscription := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "Subscription",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"annotations": map[string]interface{}{
					"cpaas.io/target-namespaces": "",
				},
				"labels": map[string]interface{}{
					"catalog": "platform",
				},
			},
			"spec": map[string]interface{}{
				"channel":             channel,
				"installPlanApproval": "Manual",
				"name":                name,
				"source":              "platform",
				"sourceNamespace":     systemNamespace,
				"startingCSV":         csv,
			},
		},
	}

	// Retry creation up to 3 times with exponential backoff
	var result *unstructured.Unstructured
	for attempt := 1; attempt <= 3; attempt++ {
		result, err = o.client.Resource(subscriptionGVR).Namespace(namespace).Create(ctx, subscription, metav1.CreateOptions{})
		if err == nil {
			return result, nil
		}

		// If this is not the last attempt, wait before retrying
		if attempt < 3 {
			// Exponential backoff: 1s, 2s, 4s
			backoffDuration := time.Duration(1<<uint(attempt-1)) * time.Second
			log.Infow("subscription creation failed, retrying",
				"attempt", attempt,
				"error", err.Error(),
				"backoff", backoffDuration.String())
			time.Sleep(backoffDuration)
		}
	}

	// All attempts failed
	return nil, fmt.Errorf("failed to create subscription after 3 attempts: %v", err)
}

// waitInstallPlanForCSV waits until Subscription.status.installplan.name
// points at an InstallPlan whose spec.clusterServiceVersionNames contains the
// target CSV, then returns that InstallPlan name.
//
// Why the extra CSV check (vs. the old "any installplan name will do"
// behaviour): on the second+ upgrade step Subscription.status.installplan.name
// briefly still references the *previous* InstallPlan that we already
// approved. Returning it would make the caller no-op-approve the old plan and
// then hang in waitCSVReady waiting for a CSV transition that OLM never
// produces. Matching on spec.clusterServiceVersionNames is the only way the
// API tells us which CSV a given InstallPlan targets, so we poll until the
// referenced plan actually targets the version we just pushed.
func (o *Operator) waitInstallPlanForCSV(ctx context.Context, subName, namespace, csv string) (string, error) {
	log := logging.FromContext(ctx)
	var matched string

	err := wait.PollUntilContextTimeout(ctx, o.interval, o.timeout, true, func(ctx context.Context) (done bool, err error) {
		sub, err := o.client.Resource(subscriptionGVR).Namespace(namespace).Get(ctx, subName, metav1.GetOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return false, err
		}
		if sub == nil {
			return false, nil
		}

		ipName, _, _ := unstructured.NestedString(sub.Object, "status", "installplan", "name")
		if ipName == "" {
			return false, nil
		}

		ip, err := o.client.Resource(installPlanGVR).Namespace(namespace).Get(ctx, ipName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}

		csvs, _, _ := unstructured.NestedStringSlice(ip.Object, "spec", "clusterServiceVersionNames")
		for _, name := range csvs {
			if name == csv {
				matched = ipName
				return true, nil
			}
		}
		log.Debugw("install plan does not target desired csv yet", "installPlan", ipName, "wantCSV", csv, "haveCSVs", csvs)
		return false, nil
	})

	if err != nil {
		return "", fmt.Errorf("timeout waiting for install plan targeting csv %s on subscription %s: %w", csv, subName, err)
	}
	return matched, nil
}

func (o *Operator) waitCSVReady(ctx context.Context, name, namespace string) error {
	err := wait.PollUntilContextTimeout(ctx, o.interval, o.timeout, true, func(ctx context.Context) (done bool, err error) {
		csv, err := o.client.Resource(csvGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return false, err
		}

		if csv == nil {
			return false, nil
		}

		status, _, _ := unstructured.NestedMap(csv.Object, "status")
		if phase, ok := status["phase"].(string); ok && phase == "Succeeded" {
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		return fmt.Errorf("timeout waiting for csv %s to be ready, error: %s", name, err.Error())
	}

	return nil
}

func (o *Operator) DeleteSubscription(ctx context.Context, name, namespace string) error {
	return o.client.Resource(subscriptionGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}
