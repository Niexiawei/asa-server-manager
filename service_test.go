package main

import (
	"testing"
)

// TestServiceName checks that the service name constants are properly defined
func TestServiceName(t *testing.T) {
	if ServiceName == "" {
		t.Error("ServiceName should not be empty")
	}

	if ServiceDisplayName == "" {
		t.Error("ServiceDisplayName should not be empty")
	}

	if ServiceDescription == "" {
		t.Error("ServiceDescription should not be empty")
	}
}
