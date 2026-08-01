// Package apiresp holds response types and helpers shared across webapi domain handlers.
package apiresp

import (
	"fmt"
	"strings"
)

// StatusResponse is the standard API response envelope used by all handlers.
type StatusResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// ValidateInstanceName checks for path traversal attacks in an instance name.
func ValidateInstanceName(name string) error {
	if name == "" {
		return fmt.Errorf("instance name is required")
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) || strings.ContainsAny(name, "\x00") {
		return fmt.Errorf("invalid instance name")
	}
	return nil
}
