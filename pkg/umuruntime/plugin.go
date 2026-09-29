package umuruntime

import (
	"context"

	"asa-server/pkg/problem"
)

// Plugin is the one interface every plugin implements. Everything else a
// plugin can do is an optional interface below, discovered by type
// assertion (the io.WriterTo pattern): a display provides environment
// leases, a VC++ installer provisions prefixes, and neither has to stub out
// the other's methods.
type Plugin interface {
	// Name identifies the plugin in logs, diagnostics and Status. Unique
	// within a Host.
	Name() string
	Provides() []Capability
	Needs() []Need
}

// EnvProvider provides a capability by adding something to a launched
// process's environment (DISPLAY, for a display plugin).
//
// Probe and Acquire are separate on purpose, and that separation is the
// contract: self-checks and diagnostics may only ask Probe, which must be
// read-only, offline and side-effect free. Acquire may start processes (a
// managed Xvfb). Folding them together once meant a status endpoint could
// fork an X server just by being asked — see docs/XVFB_DISPLAY_PLAN.md.
type EnvProvider interface {
	Plugin
	// Probe reports whether Acquire has a reasonable chance of succeeding.
	// detail says what would be used when ok ("自管 Xvfb 虚拟显示…"), and why
	// not when not.
	Probe() (ok bool, detail string)
	// Acquire returns a lease on the capability. When the host simply has no
	// way to provide it, the error must wrap ErrCapabilityUnavailable (see
	// CapabilityUnavailableError) — that is what lets the Host fall through to
	// the next provider and lets callers tell "this machine can't" apart from
	// "this attempt failed".
	Acquire(ctx context.Context) (Lease, error)
}

// Lease is an acquired capability, applied to one launch's environment.
type Lease interface {
	// Apply returns env with the lease's variables applied. Must return a
	// new slice: one lease may be applied to several commands.
	Apply(env []string) []string
	// Describe says, for the log, what was acquired ("自管 Xvfb 虚拟显示 :100").
	Describe() string
	// Release gives the lease back. A no-op for process-wide singletons
	// such as the managed display, which outlive every launch.
	Release()
}

// Preflighter contributes host self-check problems. Read-only.
type Preflighter interface {
	Plugin
	Preflight() []problem.Problem
}

// Statuser reports a plugin's state for diagnostics. Read-only: it must
// never acquire anything (same rule as EnvProvider.Probe).
type Statuser interface {
	Plugin
	Report() Status
}

// Closer releases whatever a plugin holds for the process's lifetime (the
// managed Xvfb). Called on process exit, in reverse dependency order.
type Closer interface {
	Plugin
	Close()
}

// Status is one plugin's diagnostic snapshot.
type Status struct {
	Ready  bool   `json:"ready"`
	Detail string `json:"detail,omitempty"`
	// Data is the plugin's own structured detail (the display plugin puts its
	// Info here). Callers that know the plugin type-assert it; generic
	// callers just serialise it.
	Data any `json:"data,omitempty"`
}

// PluginStatus is Status plus the plugin's declared shape.
type PluginStatus struct {
	Name     string       `json:"name"`
	Provides []Capability `json:"provides"`
	Needs    []Need       `json:"needs,omitempty"`
	Status
}

// Criticality says whether a plugin's failure fails the operation that ran
// it. It is given at registration by the application, not declared by the
// plugin: "VC++ failing is not an environment failure" is this program's
// judgement (most users never enable ArkApi), and another program could
// reasonably judge the opposite.
type Criticality int

const (
	// Required: a failure fails the operation (Host.Ensure).
	Required Criticality = iota
	// Optional: a failure is reported and the operation carries on.
	Optional
)

// Registered is a plugin plus how the application wants it treated.
type Registered struct {
	Plugin      Plugin
	Criticality Criticality
}

// With registers p with the given criticality.
func With(p Plugin, c Criticality) Registered { return Registered{Plugin: p, Criticality: c} }

// PrefixInspector reports a plugin's state in one Wine prefix, for
// diagnostics. Read-only, like Statuser — and when a plugin implements both,
// Host.Status asks this one about the shared prefix.
type PrefixInspector interface {
	Plugin
	InspectPrefix(ic InspectContext) Status
}

// InspectContext is what an inspection may look at.
type InspectContext struct {
	// Prefix is the Wine prefix directory.
	Prefix string
	// ExeDir is the directory of the executable that would run in it; empty
	// when there isn't one in mind. Relevant to anything DLL-shaped: Windows
	// resolves a DLL from the application directory before system32.
	ExeDir string
	// Probe answers, read-only, whether a capability could be acquired
	// (see Host.Probe). Never nil when the Host builds the context.
	Probe func(Capability) (ok bool, detail string)
}

// OutcomeKind classifies what provisioning a prefix did.
type OutcomeKind int

const (
	// Done: the plugin changed the prefix and the capability is in place.
	Done OutcomeKind = iota
	// AlreadySatisfied: nothing needed doing.
	AlreadySatisfied
	// Degraded: part of the work was skipped for a reason that is not a
	// failure (the VC++ installer without a display); Cause says why.
	Degraded
	// Skipped: the plugin did nothing — disabled, or a hard dependency is
	// unavailable (Cause is then a *CapabilityUnavailableError).
	Skipped
	// Failed: the plugin returned an error, which is Cause.
	Failed
)

func (k OutcomeKind) String() string {
	switch k {
	case Done:
		return "done"
	case AlreadySatisfied:
		return "already-satisfied"
	case Degraded:
		return "degraded"
	case Skipped:
		return "skipped"
	case Failed:
		return "failed"
	}
	return "unknown"
}

// Outcome is one plugin's result of provisioning one prefix. It is how a
// failure of an Optional plugin reaches the application (Config.OnOutcome)
// without failing the operation, and how "not a failure, but you should know"
// results reach it at all.
type Outcome struct {
	Plugin string
	// Key is the prefix's key ("" = the shared one).
	Key   string
	Kind  OutcomeKind
	Cause error
	// Detail is the plugin's own structured result (the VC++ plugin puts
	// its vcredist.Result here).
	Detail any
}
