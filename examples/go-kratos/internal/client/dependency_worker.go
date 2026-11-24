package client

import (
    "context"
    "fmt"
    "net/http"
    "net/url"

    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type DependencyWorkerClient struct {
    endpoint string
    client   *http.Client
}

func NewDependencyWorkerClient(endpoint string) *DependencyWorkerClient {
    return &DependencyWorkerClient{
        endpoint: endpoint,
        client:   &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
    }
}

func (c *DependencyWorkerClient) Call(ctx context.Context, mode string) error {
    requestURL := c.endpoint + "/dependency/" + url.PathEscape(mode)
    request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
    if err != nil { return fmt.Errorf("create dependency worker request: %w", err) }
    response, err := c.client.Do(request)
    if err != nil { return fmt.Errorf("call dependency worker: %w", err) }
    defer response.Body.Close()
    if response.StatusCode >= http.StatusBadRequest {
        return fmt.Errorf("dependency worker returned status %d", response.StatusCode)
    }
    return nil
}