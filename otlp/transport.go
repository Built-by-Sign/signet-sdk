// Package otlp defines shared transport settings for OTLP providers.
package otlp

import (
	"crypto/tls"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Endpoint is a validated OTLP destination. HTTP paths are base paths shared by signals.
type Endpoint struct {
	Host     string
	BasePath string
	HTTP     bool
	Secure   bool
}

// ParseEndpoint requires an explicit grpc, grpcs, http, or https scheme.
func ParseEndpoint(raw string) (Endpoint, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Endpoint{}, fmt.Errorf("signet: invalid OTLP endpoint URL")
	}
	e := Endpoint{Host: u.Host, BasePath: strings.TrimRight(u.Path, "/")}
	switch u.Scheme {
	case "grpc":
		e.HTTP, e.Secure = false, false
	case "grpcs":
		e.HTTP, e.Secure = false, true
	case "http":
		e.HTTP, e.Secure = true, false
	case "https":
		e.HTTP, e.Secure = true, true
	default:
		return Endpoint{}, fmt.Errorf("signet: OTLP endpoint requires grpc://, grpcs://, http://, or https://")
	}
	if u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return Endpoint{}, fmt.Errorf("signet: OTLP endpoint requires a host and must not contain credentials, query, or fragment")
	}
	if strings.HasSuffix(u.Host, ":") {
		return Endpoint{}, fmt.Errorf("signet: OTLP endpoint has an empty port")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return Endpoint{}, fmt.Errorf("signet: invalid OTLP endpoint port")
		}
	}
	if !e.HTTP && u.Path != "" && u.Path != "/" {
		return Endpoint{}, fmt.Errorf("signet: gRPC endpoint must not contain a path")
	}
	return e, nil
}

// ResolveTLS validates settings and clones caller-owned TLS configuration.
// Secure schemes require explicit RootCAs containing at least one certificate.
// Client certificates or GetClientCertificate enable mTLS without a separate mode.
func ResolveTLS(endpoint string, cfg *tls.Config) (*tls.Config, error) {
	if endpoint == "" {
		if cfg != nil {
			return nil, fmt.Errorf("signet: OTLP TLS config requires an endpoint")
		}
		return nil, nil
	}
	e, err := ParseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if !e.Secure {
		if cfg != nil {
			return nil, fmt.Errorf("signet: OTLP TLS config conflicts with plaintext endpoint scheme")
		}
		return nil, nil
	}
	if cfg == nil {
		return nil, fmt.Errorf("signet: TLS endpoint requires an OTLP TLS config")
	}
	if cfg.RootCAs == nil || len(cfg.RootCAs.Subjects()) == 0 {
		return nil, fmt.Errorf("signet: OTLP TLS RootCAs must contain at least one CA certificate")
	}
	return cfg.Clone(), nil
}

// ResolveHeaders validates and copies explicit headers, normalizing names to lowercase
// for HTTP and gRPC. nil preserves exporter environment settings; an empty map
// explicitly clears them. Names use the common HTTP/gRPC character set and values
// must be printable ASCII. Credentials are never included in validation errors.
func ResolveHeaders(headers map[string]string) (map[string]string, error) {
	if headers == nil {
		return nil, nil
	}
	result := make(map[string]string, len(headers))
	for key, value := range headers {
		name := strings.ToLower(key)
		if name == "" || strings.ContainsFunc(name, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
		}) {
			return nil, fmt.Errorf("signet: invalid OTLP header name")
		}
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("signet: duplicate OTLP header names ignoring case")
		}
		if strings.ContainsFunc(value, func(r rune) bool { return r < 32 || r > 126 }) {
			return nil, fmt.Errorf("signet: OTLP header values must contain only printable ASCII characters")
		}
		result[name] = value
	}
	return result, nil
}
