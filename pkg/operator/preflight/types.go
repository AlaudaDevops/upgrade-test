// Package preflight holds the value types used by OperatorInterface.PreflightBaseline.
// It is a leaf package so that both the operator interface (pkg/operator) and
// concrete implementations (pkg/operator/operatorhub, pkg/operator/local) can
// import it without creating an import cycle.
package preflight

// Residual is a single OLM-side resource that PreflightBaseline detected as
// still present in the target cluster. It is a value type intentionally —
// callers (cmd layer) accumulate them across paths and format the final user
// report without needing to type-assert any operator-package internals.
//
// RecommendedCleanup is pre-rendered so the cmd layer does not have to know
// per-resource cleanup conventions; operator implementations own that knowledge.
// The pre-rendered string must have its `name` / `namespace` segments already
// shell-escaped (via %q) because users copy-paste it verbatim into a shell.
type Residual struct {
	Kind               string
	Namespace          string
	Name               string
	RecommendedCleanup string
}
