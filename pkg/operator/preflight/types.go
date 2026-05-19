// Package preflight holds the value types used by OperatorInterface.PreflightBaseline.
// It is a leaf package so that both the operator interface (pkg/operator) and
// concrete implementations (pkg/operator/operatorhub, pkg/operator/local) can
// import it without creating an import cycle.
package preflight

import (
	"fmt"
	"strings"
)

// Residual is a single OLM-side resource that PreflightBaseline detected as
// still present in the target cluster. It is a value type intentionally —
// callers (cmd layer) accumulate them across paths and format the final user
// report without needing to type-assert any operator-package internals.
//
// Construct via NewResidual; the constructor is the single source of truth
// for `RecommendedCleanup` formatting and shell-quoting, so individual
// operator implementations never re-implement that template (and never
// accidentally skip the quoting).
type Residual struct {
	Kind               string
	Namespace          string
	Name               string
	RecommendedCleanup string
}

// NewResidual constructs a Residual with `RecommendedCleanup` filled in from
// the standard `kubectl delete <kind> <name> -n <namespace>` template, with
// name and namespace `%q`-quoted so a user copy-pasting the line into a shell
// gets sane behavior for any K8s-valid (DNS-1123) identifier.
//
// Kind is lower-cased to match the kubectl noun form (`subscription`, not
// `Subscription`); the Kind field on the struct keeps its capitalised form
// for the human-facing report.
func NewResidual(kind, namespace, name string) Residual {
	return Residual{
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
		RecommendedCleanup: fmt.Sprintf(
			"kubectl delete %s %q -n %q",
			strings.ToLower(kind), name, namespace,
		),
	}
}
