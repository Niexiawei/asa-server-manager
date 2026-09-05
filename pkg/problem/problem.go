// Package problem defines the shared self-check result type used by every
// runtime-dependency checker (Xvfb, VC++ Redist, Wine prefix, ACLs, ...): a
// named failure, its human-readable detail, an optional fix hint, and whether
// it merely degrades the feature (Warning) or blocks it outright.
package problem

// Problem is one failed self-check.
type Problem struct {
	Name   string // short id, e.g. "glibc32"
	Detail string // human-readable description of what's missing/wrong
	Fix    string // suggested remediation command, if any ("" when there isn't one)
	// Warning marks an advisory rather than a blocker: the thing still works,
	// just in a degraded or less convenient form. Consumers must treat the two
	// differently — `asa-server setup` refuses to continue on a blocker but not
	// on an advisory, and the preflight API reports healthy when only
	// advisories are present.
	//
	// Without this distinction every check is a hard stop, which is how
	// "the acl package isn't installed" once became a reason `setup` would not
	// run at all — see docs/ACL_PERMISSION_HARDENING_PLAN.md §1.
	Warning bool
}

// Blockers returns the subset of problems that must stop whatever is being
// attempted; Advisories returns the rest.
func Blockers(problems []Problem) []Problem { return filterProblems(problems, false) }

// Advisories returns the subset of problems that are merely recommendations.
func Advisories(problems []Problem) []Problem { return filterProblems(problems, true) }

func filterProblems(problems []Problem, warning bool) []Problem {
	var out []Problem
	for _, p := range problems {
		if p.Warning == warning {
			out = append(out, p)
		}
	}
	return out
}
