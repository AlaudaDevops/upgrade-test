package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	upctx "github.com/AlaudaDevops/tools-upgrade-test/pkg/context"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// InstallOperator installs the operator
// Parameters:
//   - version: The version of the operator to install
//   - name: The name of the operator
//
// Returns:
//   - error: Any error that occurred during the operation
func (o *Operator) InstallOperator(ctx context.Context, operatorName, csvName string) error {
	logger := upctx.LoggerFromContext(ctx)
	logger.Info("starting operator installation",
		zap.String("operatorName", operatorName),
		zap.String("csvName", csvName),
		zap.String("namespace", upctx.OperatorNamespaceFromContext(ctx)))

	installSuccess := false
	maxRetries := upctx.MaxRetriesFromContext(ctx)
	var err error
	for i := 0; i < maxRetries; i++ {
		time.Sleep(2 * time.Second)
		if err = o.CreateSubscription(ctx, operatorName, csvName); err != nil {
			logger.Warnw("failed to create subscription",
				"operatorName", operatorName,
				"csvName", csvName,
				"error", err,
				"attempt", i+1)
			continue
		}

		// Get InstallPlan name from Subscription status
		var installPlanName string
		installPlanName, err = o.getInstallPlanNameFromSubscription(ctx, operatorName)
		if err != nil {
			logger.Errorw("failed to get install plan name",
				"operatorName", operatorName,
				"error", err)
			return err
		}

		if installPlanName == "" {
			logger.Infow("install plan name not found, retrying",
				"operatorName", operatorName,
				"csvName", csvName,
				"attempt", i+1)
			continue
		}
		// Check and approve InstallPlan
		err = o.checkAndApproveInstallPlan(ctx, installPlanName)
		if err != nil {
			logger.Errorw("failed to check and approve install plan",
				"installPlanName", installPlanName,
				"error", err)
			return err
		}

		err = o.waitCSVReady(ctx, csvName)
		if err != nil {
			logger.Warnw("failed to wait for csv to be ready",
				"csvName", csvName,
				"error", err,
				"attempt", i+1)
			continue
		}

		installSuccess = true
		break
	}

	if installSuccess {
		logger.Infow("operator installed successfully",
			"operatorName", operatorName,
			"csvName", csvName)
		return nil
	}

	return fmt.Errorf("failed to install operator after %d retries", maxRetries)
}

func (o *Operator) waitCSVReady(ctx context.Context, name string) error {
	logger := upctx.LoggerFromContext(ctx)
	logger.Infow("waiting for csv to be ready",
		"csv", name,
		"namespace", upctx.OperatorNamespaceFromContext(ctx))

	operatorNamespace := upctx.OperatorNamespaceFromContext(ctx)
	csv, err := o.client.Resource(csvGVR).Namespace(operatorNamespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		logger.Errorw("failed to get csv",
			"csv", name,
			"namespace", operatorNamespace,
			"error", err)
		return err
	}

	status, found, err := unstructured.NestedMap(csv.Object, "status")
	if err != nil || !found {
		logger.Errorw("failed to get csv status",
			"csv", name,
			"error", err)
		return err
	}

	phase, found, err := unstructured.NestedString(status, "phase")
	if err != nil || !found {
		logger.Errorw("failed to get csv phase",
			"csv", name,
			"error", err)
		return err
	}

	if phase != "Succeeded" {
		logger.Infow("csv not ready",
			"csv", name,
			"phase", phase)
		return fmt.Errorf("csv not ready, phase: %s", phase)
	}

	logger.Infow("csv ready",
		"csv", name,
		"phase", phase)
	return nil
}

// ensureNamespace ensures the operator namespace exists
// If the namespace doesn't exist, it will be created
// Returns:
//   - error: Any error that occurred during the operation
func (o *Operator) ensureNamespace(ctx context.Context) error {
	logger := upctx.LoggerFromContext(ctx)
	operatorNamespace := upctx.OperatorNamespaceFromContext(ctx)

	_, err := o.client.Resource(namespaceGVR).Get(ctx, operatorNamespace, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	if err == nil {
		return nil
	}

	ns := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name": operatorNamespace,
			},
		},
	}

	_, err = o.client.Resource(namespaceGVR).Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		logger.Errorw("failed to create namespace",
			"namespace", operatorNamespace,
			"error", err)
		return err
	}
	logger.Infow("namespace created successfully",
		"namespace", operatorNamespace)
	return nil
}

// approveInstallPlan approves the InstallPlan by setting spec.approved to true
// Parameters:
//   - name: The name of the InstallPlan
//
// Returns:
//   - error: Any error that occurred during the operation
func (o *Operator) approveInstallPlan(ctx context.Context, name string) error {
	logger := upctx.LoggerFromContext(ctx)

	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"approved": true,
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return err
	}

	operatorNamespace := upctx.OperatorNamespaceFromContext(ctx)
	_, err = o.client.Resource(installPlanGVR).Namespace(operatorNamespace).Patch(
		ctx,
		name,
		types.MergePatchType,
		patchBytes,
		metav1.PatchOptions{},
	)
	if err != nil {
		return err
	}
	logger.Infow("install plan approved successfully",
		"name", name)
	return nil
}

// getInstallPlanNameFromSubscription gets the InstallPlan name from Subscription status
// Parameters:
//   - name: The name of the Subscription
//
// Returns:
//   - string: The name of the InstallPlan
//   - error: Any error that occurred during the operation
func (o *Operator) getInstallPlanNameFromSubscription(ctx context.Context, name string) (string, error) {
	logger := upctx.LoggerFromContext(ctx)
	logger.Infow("getting install plan name from subscription",
		"subscription", name)

	operatorNamespace := upctx.OperatorNamespaceFromContext(ctx)
	sub, err := o.client.Resource(subscriptionGVR).Namespace(operatorNamespace).Get(
		ctx,
		name,
		metav1.GetOptions{},
	)
	if err != nil {
		logger.Errorw("failed to get subscription",
			"subscription", name,
			"error", err)
		return "", err
	}

	status, found, err := unstructured.NestedMap(sub.Object, "status")
	if err != nil || !found {
		logger.Errorw("failed to get subscription status",
			"subscription", name,
			"error", err)
		return "", err
	}

	installPlanRef, found, err := unstructured.NestedMap(status, "installPlanRef")
	if err != nil || !found {
		logger.Errorw("failed to get install plan ref",
			"subscription", name,
			"error", err)
		return "", err
	}

	installPlanName, found, err := unstructured.NestedString(installPlanRef, "name")
	if err != nil || !found {
		logger.Errorw("failed to get install plan name",
			"subscription", name,
			"error", err)
		return "", err
	}

	logger.Infow("found install plan name",
		"installPlanName", installPlanName)
	return installPlanName, nil
}

// checkAndApproveInstallPlan checks if InstallPlan exists and approves it
// Parameters:
//   - name: The name of the InstallPlan
//
// Returns:
//   - error: Any error that occurred during the operation
func (o *Operator) checkAndApproveInstallPlan(ctx context.Context, name string) error {
	operatorNamespace := upctx.OperatorNamespaceFromContext(ctx)
	installPlan, err := o.client.Resource(installPlanGVR).Namespace(operatorNamespace).Get(
		ctx,
		name,
		metav1.GetOptions{},
	)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	approved, found, err := unstructured.NestedBool(installPlan.Object, "spec", "approved")
	if err != nil || !found {
		return err
	}

	if approved {
		return nil
	}

	return o.approveInstallPlan(ctx, name)
}

func (o *Operator) ensurePackageManifest(ctx context.Context, name string) bool {
	systemNamespace := upctx.SystemNamespaceFromContext(ctx)
	_, err := o.client.Resource(packageManifestGVR).Namespace(systemNamespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false
		}
		return false
	}
	return true
}

// CreateSubscription creates a Subscription resource with the given version and name
// If the resource already exists, it will be ignored
// Parameters:
//   - version: The version of the operator to install
//   - name: The name of the operator
//
// Returns:
//   - error: Any error that occurred during the creation, except for AlreadyExists error
func (o *Operator) CreateSubscription(ctx context.Context, operatorName, startCSVName string) error {
	logger := upctx.LoggerFromContext(ctx)

	// Ensure namespace exists first
	if err := o.ensureNamespace(ctx); err != nil {
		return err
	}

	if !o.ensurePackageManifest(ctx, operatorName) {
		return fmt.Errorf("[%s] package manifest not found", operatorName)
	}

	operatorNamespace := upctx.OperatorNamespaceFromContext(ctx)
	subscription, err := o.client.Resource(subscriptionGVR).Namespace(operatorNamespace).Get(ctx, operatorName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get subscription: %v", err)
	}

	if err == nil && subscription != nil {
		logger.Infow("subscription already exists, skipping creation", "name", operatorName)
		return nil
	}

	systemNamespace := upctx.SystemNamespaceFromContext(ctx)
	subscription = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "Subscription",
			"metadata": map[string]interface{}{
				"name":      operatorName,
				"namespace": operatorNamespace,
				"labels": map[string]interface{}{
					"catalog": "custom",
				},
			},
			"spec": map[string]interface{}{
				"channel":             "stable",
				"installPlanApproval": "Manual",
				"name":                operatorName,
				"source":              "custom",
				"sourceNamespace":     systemNamespace,
				"startingCSV":         startCSVName,
			},
		},
	}

	_, err = o.client.Resource(subscriptionGVR).Namespace(operatorNamespace).Create(ctx, subscription, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			logger.Infow("subscription already exists", "name", operatorName)
			return nil
		}
		logger.Errorw("failed to create subscription", "error", err)
		return err
	}
	logger.Infow("subscription created successfully", "name", operatorName)
	return nil
}
