package signet

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
)

// buildResource creates an OTel Resource with standard service attributes.
func buildResource(cfg Config) (*resource.Resource, error) {
	instanceID := cfg.InstanceID
	if instanceID == "" {
		instanceID, _ = os.Hostname()
	}

	attrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceInstanceID(instanceID),
	}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(cfg.ServiceVersion))
	}
	if cfg.Environment != "" {
		attrs = append(attrs, semconv.DeploymentEnvironmentName(cfg.Environment))
	}

	return resource.New(context.Background(),
		resource.WithAttributes(attrs...),
		resource.WithProcessPID(),
		resource.WithTelemetrySDK(), // telemetry.sdk.name/language/version
	)
}
