package problem

import "testing"

// Blockers/Advisories exist so a degraded-but-working condition can be
// reported without turning into a hard stop. `asa-server setup` refuses to
// continue on a blocker, so misclassifying an advisory means a perfectly
// usable machine cannot be set up at all — see
// docs/ACL_PERMISSION_HARDENING_PLAN.md §1.
func TestBlockersAndAdvisories(t *testing.T) {
	problems := []Problem{
		{Name: "glibc32"},
		{Name: "posix-acl", Warning: true},
		{Name: "python3"},
		{Name: "something-else", Warning: true},
	}

	blockers := Blockers(problems)
	if len(blockers) != 2 || blockers[0].Name != "glibc32" || blockers[1].Name != "python3" {
		t.Errorf("Blockers = %v, want the two non-warning entries in order", blockers)
	}

	advisories := Advisories(problems)
	if len(advisories) != 2 || advisories[0].Name != "posix-acl" {
		t.Errorf("Advisories = %v, want the two warning entries in order", advisories)
	}
}

func TestBlockersAndAdvisoriesEdgeCases(t *testing.T) {
	if got := Blockers(nil); got != nil {
		t.Errorf("Blockers(nil) = %v, want nil", got)
	}
	if got := Advisories(nil); got != nil {
		t.Errorf("Advisories(nil) = %v, want nil", got)
	}

	// All-advisory is the case that matters: setup must see zero blockers here
	// and carry on.
	onlyWarnings := []Problem{{Name: "posix-acl", Warning: true}}
	if got := Blockers(onlyWarnings); len(got) != 0 {
		t.Errorf("Blockers(only warnings) = %v, want empty", got)
	}
	if got := Advisories(onlyWarnings); len(got) != 1 {
		t.Errorf("Advisories(only warnings) = %v, want 1 entry", got)
	}
}
