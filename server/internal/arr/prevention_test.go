package arr

import (
	"strings"
	"testing"
)

// reachableProblemLabels is every label a real diagnosis can persist as
// issues.problem_kind: the rule tables plus the structural verdicts, minus
// anything below warning severity.
//
// Severity is the filter that matters. An auto issue is only opened when its
// group carries a warning or error signal, so an info-severity label never
// reaches issues.problem_kind and can never recur — a catalog entry for one
// would be dead copy that no admin will ever see. This map is how that gets
// caught at test time instead of never.
func reachableProblemLabels() map[string]bool {
	out := map[string]bool{}
	for _, rule := range messageRules {
		if rule.severity == SeverityWarning || rule.severity == SeverityError {
			out[rule.problem] = true
		}
	}
	for _, rule := range errorRules {
		if rule.severity == SeverityWarning || rule.severity == SeverityError {
			out[rule.problem] = true
		}
	}
	// The structural verdicts classify() reaches without a rule table.
	out[ProblemDownloadError] = true
	out[ProblemWaitingToImport] = true
	out[ProblemDownloadFailed] = true
	out[ProblemImportBlocked] = true
	// Not a queue diagnosis at all: the pre-air detector writes this one
	// directly, and it is warning-shaped by construction.
	out[ProblemPreAirSeasonFill] = true
	return out
}

func TestEveryPreventionEntryIsForAReachableProblem(t *testing.T) {
	reachable := reachableProblemLabels()
	for label := range preventionCatalog {
		if !reachable[label] {
			t.Errorf("catalog advises on %q, which no diagnosis can persist at warning-or-error severity — the notice could never fire", label)
		}
	}
}

// The map key and the entry's own Problem field must agree, or PreventionFor
// returns advice whose header names a different problem than the one asked
// about.
func TestPreventionCatalogKeysMatchTheirEntries(t *testing.T) {
	for label, entry := range preventionCatalog {
		if entry.Problem != label {
			t.Errorf("catalog[%q].Problem = %q", label, entry.Problem)
		}
	}
}

func TestEveryPreventionEntryIsActionable(t *testing.T) {
	scopes := map[string]bool{
		PreventionScopeInstance: true,
		PreventionScopeClient:   true,
		PreventionScopeDisk:     true,
	}
	for label, entry := range preventionCatalog {
		if !scopes[entry.Scope] {
			t.Errorf("%q has scope %q, which is not one of the three", label, entry.Scope)
		}
		if strings.TrimSpace(entry.Why) == "" {
			t.Errorf("%q has no explanation of why repeating the repair will not help", label)
		}
		if len(entry.Steps) == 0 {
			t.Errorf("%q names no place to look — advice with no step is just a complaint", label)
		}
		for i, step := range entry.Steps {
			if strings.TrimSpace(step) == "" {
				t.Errorf("%q step %d is empty", label, i)
			}
		}
	}
}

// The catalog is deliberately incomplete. These labels are ordinary states or
// too generic to advise on, and an entry for one of them would train an admin
// to stop reading the advice.
func TestPreventionCatalogWithholdsAdviceItDoesNotHave(t *testing.T) {
	for _, label := range []string{
		ProblemWaitingToImport,
		ProblemAlreadyImported,
		ProblemImportBlocked,
		ProblemDownloadError,
		ProblemNotAnUpgrade,
		ProblemNotAFormatUpgrade,
		// Info severity: never opens an auto issue, so it can never recur.
		ProblemUnmonitored,
	} {
		if _, ok := PreventionFor(label); ok {
			t.Errorf("catalog advises on %q; it should stay silent", label)
		}
	}
}

func TestPreventionForUnknownLabel(t *testing.T) {
	if _, ok := PreventionFor("something a future rule invents"); ok {
		t.Error("PreventionFor invented advice for an unknown label")
	}
	if _, ok := PreventionFor(""); ok {
		t.Error("PreventionFor advised on the healthy (empty) label")
	}
}

// A spot check that the labels really are shared constants rather than two
// copies of the same string: the catalog must key off exactly what a diagnosis
// produces.
func TestPreventionKeysAreTheDiagnosisLabels(t *testing.T) {
	// Not "error": classify() matches errorRules against ErrorMessage first and
	// falls back to "Download error" when nothing matches, which never reaches
	// the status-message rules at all.
	sig := QueueSignal{
		TrackedDownloadStatus: "warning",
		StatusMessages: []StatusMessage{
			{Title: "import", Messages: []string{"Not enough free space to import"}},
		},
	}
	d := Diagnose(sig)
	if d.Problem != ProblemNoFreeSpace {
		t.Fatalf("diagnosis produced %q, want the shared constant", d.Problem)
	}
	if _, ok := PreventionFor(d.Problem); !ok {
		t.Fatal("a live diagnosis label did not find its own catalog entry")
	}
}
