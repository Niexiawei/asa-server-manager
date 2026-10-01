// Package apiresp holds response types and helpers shared across webapi domain handlers.
package apiresp

import (
	cfgpkg "asa-server/internal/config"
)

// StatusResponse is the standard API response envelope used by all handlers.
type StatusResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// ValidateInstanceName checks for path traversal attacks in an instance name.
// The rule lives in internal/config so non-HTTP callers (arkapimanage) can
// share it; see cfgpkg.ValidateInstanceName.
func ValidateInstanceName(name string) error {
	return cfgpkg.ValidateInstanceName(name)
}
