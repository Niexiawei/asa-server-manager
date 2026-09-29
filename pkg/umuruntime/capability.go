// Package umuruntime is the host for running Windows executables under
// umu-launcher + GE-Proton on Linux: it orchestrates the runtime mechanisms
// in asa-server/pkg/umu (download, prefix warming, launch environment) and
// asa-server/pkg/wineprefix (prefix layout and lifecycle), and hosts
// **plugins** — components such as a virtual X display or the Microsoft
// VC++ runtime that some executables need and others don't.
//
// A plugin declares what it provides and what it needs, both as
// Capabilities; the host resolves the dependency graph, orders setup and
// launch-time acquisition accordingly, and aggregates their self-checks and
// diagnostics. Plugins are ordinary Go values registered at compile time —
// not Go's `plugin` package (.so), which does not exist on Windows and would
// require the host and plugins to be built with identical toolchains.
//
// Like its dependencies, this package knows nothing about ASA, instances or
// config.yaml: user-facing guidance ("run asa-server setup") is the caller's
// job, and every error it produces for that purpose is typed.
//
// This file and graph.go/plugin.go/errors.go carry no build tag: the
// capability vocabulary is platform-neutral (a Windows exe's needs are the
// same wherever it runs), so cross-platform callers can express requirements
// without their own build tags, and the dependency graph is unit-tested on
// every platform. See docs/UMU_RUNTIME_PLUGIN_PLAN.md.
package umuruntime

// Capability names something a launched Windows executable needs from its
// environment. Capabilities are named by the **need** ("can create Win32
// windows"), never by the component that satisfies it ("Xvfb"): the display
// may come from a managed Xvfb, the host's own X server or something else
// entirely, and a consumer must not care which. See
// docs/UMU_RUNTIME_PLUGIN_PLAN.md §4.1.
type Capability string

const (
	// CapGUI: the executable can create Win32 windows. On Windows the
	// window station satisfies it natively; under Wine it means a reachable
	// X display — without one every CreateWindow fails, silently killing
	// anything that opens a window (ArkApi's loader, Microsoft's installers).
	CapGUI Capability = "win32.gui"
	// CapMSVCRT: the native Microsoft VC++ 2015-2022 runtime can be loaded.
	// A system component on Windows; under Wine it has to be installed into
	// (or overridden in) the prefix.
	CapMSVCRT Capability = "win32.msvcrt"
)

// Phase says when a Need has to be satisfied.
type Phase int

const (
	// PhaseLaunch: every time an executable is launched (a display lease).
	PhaseLaunch Phase = iota
	// PhaseProvision: while installing something into a Wine prefix (the
	// VC++ installer needs a display to run at all).
	PhaseProvision
)

func (p Phase) String() string {
	switch p {
	case PhaseLaunch:
		return "launch"
	case PhaseProvision:
		return "provision"
	}
	return "unknown"
}

// Need is one dependency a plugin declares.
type Need struct {
	Cap   Capability
	Phase Phase
	// Soft: the plugin still runs without it and degrades on its own. A soft
	// need only orders the graph (its provider goes first); it may have no
	// provider registered at all.
	Soft bool
}

// Readiness is a capability's state in one context (a prefix, the host).
type Readiness struct {
	OK bool
	// Definitive is false when the judgement is a heuristic (the VC++ PE
	// header check is one). Callers should warn on a non-definitive
	// negative, never block on it.
	Definitive bool
	// Detail is the mechanism-level reason, meant to be wrapped by the
	// caller's own wording.
	Detail string
}
