package operator

import (
	"context"
	"fmt"
	"time"

	upctx "github.com/AlaudaDevops/tools-upgrade-test/pkg/context"
	"github.com/oliveagle/jsonpath"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
)

func (o *Operator) InstallSubscription(ctx context.Context, csv string) error {
	if csv == "" {
		return fmt.Errorf("csv is empty")
	}

	log := upctx.LoggerFromContext(ctx)
	log.Infow("installing subscription", "csv", csv, "namespace", o.namespace)
	// Delete the subscription and csv if they exist
	if err := o.client.Resource(subscriptionGVR).Namespace(o.namespace).Delete(ctx, o.name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete old subscription: %v", err)
	}

	if err := o.deleteCSV(ctx, csv, o.namespace); err != nil {
		return fmt.Errorf("failed to delete old csv: %v", err)
	}

	log.Infow("creating subscription", "name", o.name, "namespace", o.namespace, "csv", csv)
	_, err := o.createSubscription(ctx, o.name, o.namespace, csv)
	if err != nil {
		return fmt.Errorf("failed to create subscription: %v", err)
	}

	log.Infow("waiting for install plan", "name", o.name, "namespace", o.namespace)
	installPlanName, err := o.waitInstallPlan(ctx, o.name, o.namespace)
	if err != nil {
		return fmt.Errorf("failed to wait for install plan: %v", err)
	}

	log.Infow("approving install plan", "name", o.name, "namespace", o.namespace, "installPlanName", installPlanName)
	installPlan, err := o.client.Resource(installPlanGVR).Namespace(o.namespace).Get(ctx, installPlanName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get install plan: %v", err)
	}

	installPlan.Object["spec"].(map[string]interface{})["approved"] = true
	_, err = o.client.Resource(installPlanGVR).Namespace(o.namespace).Update(ctx, installPlan, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update install plan: %v", err)
	}

	log.Infow("waiting for csv to be ready", "name", csv, "namespace", o.namespace)
	err = o.waitCSVReady(ctx, csv, o.namespace)
	if err != nil {
		return fmt.Errorf("failed to wait for csv to be ready: %v", err)
	}

	log.Infow("subscription installed successfully", "name", o.name, "namespace", o.namespace)
	return nil
}

func (o *Operator) createSubscription(ctx context.Context, name, namespace, csv string) (*unstructured.Unstructured, error) {
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
				"labels": map[string]interface{}{
					"catalog": "platform",
				},
			},
			"spec": map[string]interface{}{
				"channel":             "stable",
				"installPlanApproval": "Manual",
				"name":                name,
				"source":              "platform",
				"sourceNamespace":     systemNamespace,
				"startingCSV":         csv,
			},
		},
	}
	return o.client.Resource(subscriptionGVR).Namespace(namespace).Create(ctx, subscription, metav1.CreateOptions{})
}

func (o *Operator) deleteCSV(ctx context.Context, name, namespace string) error {
	o.client.Resource(csvGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	err := wait.PollUntilContextTimeout(ctx, o.interval, o.timeout, true, func(ctx context.Context) (done bool, err error) {
		_, err = o.client.Resource(csvGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("failed to delete csv %s: %v", name, err)
	}
	return nil
}

// waitInstallPlan waits for the subscription to have an install plan and returns the install plan name
func (o *Operator) waitInstallPlan(ctx context.Context, name, namespace string) (string, error) {
	var installPlanName string
	interval := 5 * time.Second
	timeout := 10 * time.Minute

	err := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (done bool, err error) {
		obj, err := o.client.Resource(subscriptionGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return false, err
		}

		if obj == nil {
			return false, nil
		}

		// Use jsonpath to extract status.installplan.name
		jsonpathQuery := "$.status.installplan.name"
		result, err := jsonpath.JsonPathLookup(obj.Object, jsonpathQuery)
		if err != nil {
			// Install plan name not found yet, continue waiting
			return false, nil
		}

		// Convert result to string
		if installPlanNameStr, ok := result.(string); ok && installPlanNameStr != "" {
			installPlanName = installPlanNameStr
			return true, nil
		}

		// Install plan name is empty or not a string, continue waiting
		return false, nil
	})

	if err != nil {
		return "", fmt.Errorf("timeout waiting for subscription %s to have install plan", name)
	}

	if installPlanName == "" {
		return "", fmt.Errorf("install plan name not found for subscription %s", name)
	}

	return installPlanName, nil
}

func (o *Operator) waitCSVReady(ctx context.Context, name, namespace string) error {
	interval := 5 * time.Second
	timeout := 10 * time.Minute

	err := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (done bool, err error) {
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
