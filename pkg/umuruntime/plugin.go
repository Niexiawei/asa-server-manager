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
	// Probe reports whether Acquire has a reasonable chance of succeeding,
	// and why not when it hasn't.
	Probe() (ok bool, why string)
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
