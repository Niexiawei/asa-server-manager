package umuruntime

import (
	"errors"
	"fmt"
	"strings"
)

// ErrCapabilityUnavailable means the host has no way to provide a capability
// at all — as opposed to having a way that failed this time. Match it with
// errors.Is; CapabilityUnavailableError carries the reason.
var ErrCapabilityUnavailable = errors.New("capability unavailable")

// ErrAutoDownloadDisabled: Host.Ensure would have to download part of the
// runtime, but automatic downloads are switched off.
var ErrAutoDownloadDisabled = errors.New("auto download is disabled and the runtime is not fully installed")

// CapabilityUnavailableError says why a capability cannot be provided on
// this host. It is ErrCapabilityUnavailable under errors.Is.
type CapabilityUnavailableError struct {
	Cap Capability
	// Why is the provider's own reason ("本机没有可用的 X 显示……"), meant to be
	// embedded in the caller's wording. Empty when no provider is registered.
	Why string
}

func (e *CapabilityUnavailableError) Error() string {
	if e.Why == "" {
		return fmt.Sprintf("%s: no provider registered", e.Cap)
	}
	return fmt.Sprintf("%s: %s", e.Cap, e.Why)
}

func (e *CapabilityUnavailableError) Is(target error) bool { return target == ErrCapabilityUnavailable }

// AcquireError: a provider exists and should have worked, but acquiring the
// capability failed this time (a managed Xvfb that would not start).
type AcquireError struct {
	Cap    Capability
	Plugin string
	Err    error
}

func (e *AcquireError) Error() string {
	return fmt.Sprintf("%s (%s): %v", e.Cap, e.Plugin, e.Err)
}

func (e *AcquireError) Unwrap() error { return e.Err }

// MissingProviderError: a plugin has a hard need nothing registered provides.
type MissingProviderError struct {
	Plugin string
	Cap    Capability
}

func (e *MissingProviderError) Error() string {
	return fmt.Sprintf("umuruntime: plugin %q needs %s, but no registered plugin provides it", e.Plugin, e.Cap)
}

// CycleError: the plugins' needs form a cycle. Path lists the plugin names
// around it, first element repeated at the end.
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return "umuruntime: plugin dependency cycle: " + strings.Join(e.Path, " -> ")
}

// DuplicatePluginError: two registered plugins share a name.
type DuplicatePluginError struct {
	Name string
}

func (e *DuplicatePluginError) Error() string {
	return fmt.Sprintf("umuruntime: plugin %q registered twice", e.Name)
}
