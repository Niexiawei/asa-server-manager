//go:build windows

package runner

import (
	"context"
	"io"
)

// Windows has no Wine prefix and no runtime plugins: a Windows exe runs
// natively, the window station gives it windows (CapGUI), and the Microsoft
// VC++ runtime is a system component that ArkApi's users install per
// ArkApi's own instructions (CapMSVCRT). Every capability is therefore
// satisfied by the platform, which is what keeps Options.Needs and
// CheckNeeds free of build tags at their call sites.
// See docs/ARKAPI_LINUX_VCREDIST_PLAN.md §3.6 and
// docs/UMU_RUNTIME_PLUGIN_PLAN.md §2.1 goal 6.

func checkNeeds(string, []Capability) []Unmet { return nil }

func provision(context.Context, string, io.Writer, []Capability) error { return nil }

func pluginStatuses() []PluginStatus { return nil }

func capabilityStatus(Capability, string, string) []PluginStatus { return nil }

func closeRuntime() {}
