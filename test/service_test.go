package test

import (
	"asa-server/winservice"
	"testing"
)

// TestServiceName checks that the service name constants are properly defined
func TestServiceName(t *testing.T) {
	if winservice.ServiceName == "" {
		t.Error("ServiceName should not be empty")
	}

	if winservice.ServiceDisplayName == "" {
		t.Error("ServiceDisplayName should not be empty")
	}

	if winservice.ServiceDescription == "" {
		t.Error("ServiceDescription should not be empty")
	}
}
