package operatorhub

import (
	"context"
	"fmt"
	"time"

	"github.com/AlaudaDevops/upgrade-test/pkg/config"
	"github.com/AlaudaDevops/upgrade-test/pkg/operator/preflight"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"knative.dev/pkg/logging"
)

// preflightTimeout caps the entire PreflightBaseline call so one slow or
// hung apiserver does not block the upgrade flow for o.timeout (default 10m).
// 30s is generous enough for 3 Get/List against a healthy apiserver while
// keeping the "fail-fast within seconds" promise honest in the typical case.
const preflightTimeout = 30 * time.Second

// installPlanTerminalPhases enumerates the InstallPlan.status.phase values
// that mark a plan as "finished" — those are historical archives, never
// residue that blocks a new upgrade. Anything else (empty, Planning,
// RequiresApproval, Installing) is considered live and blocking.
var installPlanTerminalPhases = map[string]struct{}{
	"Complete": {},
	"Failed":   {},
}

// PreflightBaseline implements OperatorInterface. See pkg/operator/interface.go
// for the contract. This method is strictly read-only: it MUST NOT call
// Create/Update/Patch/Delete on the dynamic client.
//
// Detection rules:
//   - Subscription/<name> in <namespace> — any object existing is residue.
//   - ArtifactVersion/<artifact>.<bundleVersion> in cpaas-system — same.
//   - InstallPlan in <namespace> with OLM label `operators.coreos.com/<pkg>.<ns>`,
//     non-terminal phase only.
//
// CSV detection is deliberately omitted: independent CSV residue (without a
// corresponding Sub or AV) is a degenerate state that is already covered by
// the other three checks. If a future incident shows otherwise, the plan in
// docs/plans/ documents how to add PackageManifest-driven CSV resolution.
func (o *Operator) PreflightBaseline(ctx context.Context, version config.Version) ([]preflight.Residual, error) {
	pCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()

	log := logging.FromContext(ctx)
	log.Infow("running preflight for baseline", "operator", o.name, "version", version.BundleVersion)

	checks := []struct {
		name string
		fn   func(context.Context, config.Version) ([]preflight.Residual, error)
	}{
		{"Subscription", o.checkSubscriptionResidue},
		{"ArtifactVersion", o.checkArtifactVersionResidue},
		{"InstallPlan", o.checkInstallPlanResidue},
	}

	var residuals []preflight.Residual
	for _, c := range checks {
		found, err := c.fn(pCtx, version)
		switch {
		case isTransientAPIError(err):
			return nil, fmt.Errorf("preflight: %s: %w (transient, retry the run)", c.name, err)
		case err != nil:
			return nil, fmt.Errorf("preflight: %s: %w", c.name, err)
		}
		residuals = append(residuals, found...)
	}
	return residuals, nil
}

// checkSubscriptionResidue Gets the Subscription named after the operator in
// the configured namespace. NotFound is the clean-state expectation.
func (o *Operator) checkSubscriptionResidue(ctx context.Context, _ config.Version) ([]preflight.Residual, error) {
	sub, err := o.client.Resource(subscriptionGVR).Namespace(o.namespace).Get(ctx, o.name, metav1.GetOptions{})
	switch {
	case errors.IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return []preflight.Residual{
		preflight.NewResidual("Subscription", o.namespace, sub.GetName()),
	}, nil
}

// checkArtifactVersionResidue Gets the AV named <artifact>.<bundleVersion> in
// cpaas-system. NotFound is clean state. installViaViolet's mid-upgrade
// cleanup handles AVs created during a run; preflight's job is to surface
// AVs left behind when a prior run aborted before that cleanup fired.
func (o *Operator) checkArtifactVersionResidue(ctx context.Context, version config.Version) ([]preflight.Residual, error) {
	avName := fmt.Sprintf("%s.%s", o.artifact, version.BundleVersion)
	av, err := o.client.Resource(artifactVersionGVR).Namespace(systemNamespace).Get(ctx, avName, metav1.GetOptions{})
	switch {
	case errors.IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return []preflight.Residual{
		preflight.NewResidual("ArtifactVersion", systemNamespace, av.GetName()),
	}, nil
}

// checkInstallPlanResidue Lists InstallPlans in the operator namespace using
// the OLM-managed label `operators.coreos.com/<package>.<ns>`, then filters
// out terminal phases. The label selector is the difference between scanning
// the namespace's entire historical IP archive (often hundreds of objects
// since OLM never GCs) and reading only this operator's plans.
func (o *Operator) checkInstallPlanResidue(ctx context.Context, _ config.Version) ([]preflight.Residual, error) {
	labelKey := fmt.Sprintf("operators.coreos.com/%s.%s", o.name, o.namespace)
	ips, err := o.client.Resource(installPlanGVR).Namespace(o.namespace).List(ctx, metav1.ListOptions{LabelSelector: labelKey})
	switch {
	case errors.IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, err
	}

	log := logging.FromContext(ctx)
	var residuals []preflight.Residual
	for _, ip := range ips.Items {
		phase, found, perr := unstructured.NestedString(ip.Object, "status", "phase")
		if perr != nil {
			// Type drift: status.phase is no longer a string. Surface loudly
			// rather than silently treating it as non-terminal and reporting
			// every IP in the namespace as residue. Catches OLM CRD schema
			// migrations early instead of producing a noise storm.
			return nil, fmt.Errorf("InstallPlan %s/%s: unexpected status.phase type: %w",
				ip.GetNamespace(), ip.GetName(), perr)
		}
		if !found {
			// IP exists but has not been reconciled yet (no status.phase
			// written). Treat as live — it is by definition non-terminal —
			// but log so an unexpected flood of these is observable.
			log.Infow("InstallPlan has no status.phase yet, treating as live", "name", ip.GetName())
		}
		if _, terminal := installPlanTerminalPhases[phase]; terminal {
			continue
		}
		residuals = append(residuals, preflight.NewResidual("InstallPlan", o.namespace, ip.GetName()))
	}
	return residuals, nil
}
