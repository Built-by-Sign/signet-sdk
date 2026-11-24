package signet

import (
	"testing"
)

func TestBuildResource(t *testing.T) {
	res, err := buildResource(Config{
		ServiceName:    "test-svc",
		ServiceVersion: "1.2.3",
		InstanceID:     "node-1",
		Environment:    "staging",
	})
	if err != nil {
		t.Fatalf("buildResource failed: %v", err)
	}

	attrs := res.Attributes()
	found := map[string]string{}
	for _, a := range attrs {
		found[string(a.Key)] = a.Value.AsString()
	}

	if found["service.name"] != "test-svc" {
		t.Errorf("service.name = %q, want 'test-svc'", found["service.name"])
	}
	if found["service.version"] != "1.2.3" {
		t.Errorf("service.version = %q, want '1.2.3'", found["service.version"])
	}
	if found["service.instance.id"] != "node-1" {
		t.Errorf("service.instance.id = %q, want 'node-1'", found["service.instance.id"])
	}
	if found["deployment.environment.name"] != "staging" {
		t.Errorf("deployment.environment.name = %q, want 'staging'", found["deployment.environment.name"])
	}
	if _, ok := found["deployment.environment"]; ok {
		t.Error("deprecated deployment.environment attribute should not be present")
	}
}

func TestBuildResource_DefaultHostname(t *testing.T) {
	res, err := buildResource(Config{
		ServiceName: "test-svc",
		// InstanceID left empty — should default to hostname
	})
	if err != nil {
		t.Fatalf("buildResource failed: %v", err)
	}

	attrs := res.Attributes()
	found := false
	for _, a := range attrs {
		if string(a.Key) == "service.instance.id" && a.Value.AsString() != "" {
			found = true
		}
	}
	if !found {
		t.Error("service.instance.id should default to hostname when InstanceID is empty")
	}
}
