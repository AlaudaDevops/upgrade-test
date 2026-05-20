package cmd

import (
	"fmt"
	"strings"

	"github.com/AlaudaDevops/upgrade-test/pkg/operator/preflight"
)

// PreflightError aggregates the Residuals discovered by
// OperatorInterface.PreflightBaseline across the configured upgrade paths.
// Error() renders a multi-line human report with copy-pasteable cleanup
// commands.
//
// This type is cmd-internal on purpose: the operator interface returns only
// []preflight.Residual, and the cmd layer is solely responsible for
// presentation. Keeping the formatter here means pkg/operator never grows a
// dependency on "how upgrade CLI tells the user about findings".
type PreflightError struct {
	Residuals []preflight.Residual
}

// Error formats the residuals as a sequence of cleanup blocks, then appends
// the finalizer-unstuck command template and the bypass hint.
//
// DECISION C (locked at the default — C1 / all-English): kubectl errors,
// OLM phrases, and Stack Overflow answers are all in English, so users
// copy-pasting this output into GitHub issues / Slack discover answers
// faster when our wording mirrors that vocabulary. To switch to C2
// (all-Chinese) or C3 (English keys + Chinese hints), edit this method —
// the contract (`error` interface) does not change.
func (e *PreflightError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "preflight failed: %d residual resource(s) blocking upgrade:\n\n", len(e.Residuals))
	for _, r := range e.Residuals {
		fmt.Fprintf(&b, "  %s/%s (ns: %s)\n      %s\n\n", r.Kind, r.Name, r.Namespace, r.RecommendedCleanup)
	}
	b.WriteString("If a delete hangs (finalizer stuck), patch finalizers off:\n")
	b.WriteString("  kubectl -n <ns> patch <kind> <name> --type=merge -p '{\"metadata\":{\"finalizers\":[]}}'\n\n")
	b.WriteString("After cleanup, wait ~30s for OLM to settle, then re-run `upgrade`.\n")
	b.WriteString("To bypass (NOT recommended): re-run with --skip-preflight\n")
	return b.String()
}
