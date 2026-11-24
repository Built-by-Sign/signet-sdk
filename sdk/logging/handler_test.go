package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

func TestWrapHandler_KeysAndTraceCorrelation(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(WrapHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{AddSource: true}), "svc", "1.2.3", "inst-1", "production"))

	traceID, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	spanID, _ := trace.SpanIDFromHex("b7ad6b7169203331")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	}))

	logger.InfoContext(ctx, "hello", "user", "alice")

	rec := decodeLine(t, buf.Bytes())
	checks := map[string]string{
		"level":                       "INFO",
		"msg":                         "hello",
		"service.name":                "svc",
		"deployment.environment.name": "production",
		"service.version":             "1.2.3",
		"service.id":                  "inst-1",
		"trace.id":                    "0af7651916cd43dd8448eb211c80319c",
		"span.id":                     "b7ad6b7169203331",
		"user":                        "alice",
	}
	for k, want := range checks {
		if got := rec[k]; got != want {
			t.Fatalf("%s = %v, want %v", k, got, want)
		}
	}
	stamp, ok := rec["time"].(string)
	if !ok {
		t.Fatal("missing time string")
	}
	if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
		t.Fatal(err)
	}
	source, ok := rec["source"].(map[string]any)
	if !ok || source["file"] == "" || source["function"] == "" || source["line"] == nil {
		t.Fatalf("missing structured source: %v", rec["source"])
	}
	if rec["ts"] != nil || rec["caller"] != nil {
		t.Fatal("unexpected renamed slog fields")
	}
}

func TestWrapHandler_NoTraceKeysWhenNoSpan(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(WrapHandler(slog.NewJSONHandler(&buf, nil), "svc", "1.0.0", "inst", ""))
	logger.Info("no span here")

	rec := decodeLine(t, buf.Bytes())
	if _, ok := rec["deployment.environment.name"]; ok {
		t.Fatal("empty environment should be omitted")
	}
	if v, ok := rec["trace.id"]; ok {
		t.Fatalf("trace.id = %v, want key omitted when no span", v)
	}
	if v, ok := rec["span.id"]; ok {
		t.Fatalf("span.id = %v, want key omitted when no span", v)
	}
}

func TestWrapHandler_TraceStaysTopLevelUnderGroup(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(WrapHandler(slog.NewJSONHandler(&buf, nil), "svc", "1.0.0", "inst", "production")).WithGroup("req")

	traceID, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	spanID, _ := trace.SpanIDFromHex("b7ad6b7169203331")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	}))

	logger.InfoContext(ctx, "hello", "path", "/x")

	rec := decodeLine(t, buf.Bytes())
	// trace.id / service fields must stay top-level even with an open group.
	if rec["trace.id"] != "0af7651916cd43dd8448eb211c80319c" {
		t.Fatalf("trace.id not top-level: %v", rec["trace.id"])
	}
	if rec["service.name"] != "svc" {
		t.Fatalf("service.name not top-level: %v", rec["service.name"])
	}
	if rec["deployment.environment.name"] != "production" {
		t.Fatalf("environment not at root: %v", rec)
	}
	// user attributes belong under the group.
	grp, ok := rec["req"].(map[string]any)
	if !ok {
		t.Fatalf("expected req group object, got %v", rec["req"])
	}
	if grp["path"] != "/x" {
		t.Fatalf("path under group = %v, want /x", grp["path"])
	}
}

func decodeLine(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(b), &rec); err != nil {
		t.Fatalf("log line is not valid JSON: %v (%q)", err, string(b))
	}
	return rec
}

func TestWrapHandlerLevel(t *testing.T) {
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		var buf bytes.Buffer
		logger := slog.New(WrapHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level}), "svc", "", "", ""))
		logger.Log(context.Background(), level-1, "dropped")
		if buf.Len() != 0 {
			t.Fatal("below-minimum record emitted")
		}
		logger.Log(context.Background(), level, "kept")
		if decodeLine(t, buf.Bytes())["msg"] != "kept" {
			t.Fatal("minimum level missing")
		}
	}
}

func TestWrapHandlerConcurrentContexts(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(WrapHandler(slog.NewJSONHandler(&buf, nil), "svc", "", "", "")).With("shared", true).WithGroup("request").With("bound", true)
	var wg sync.WaitGroup
	for i := 1; i <= 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tid := trace.TraceID{byte(i)}
			sid := trace.SpanID{byte(i)}
			ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid}))
			logger.InfoContext(ctx, "request", "expected", tid.String())
		}(i)
	}
	wg.Wait()
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 20 {
		t.Fatalf("records = %d", len(lines))
	}
	for _, line := range lines {
		rec := decodeLine(t, line)
		group := rec["request"].(map[string]any)
		if rec["trace.id"] != group["expected"] || group["bound"] != true {
			t.Fatalf("context leaked: %v", rec)
		}
	}
	logger.Info("no request")
	lines = bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if decodeLine(t, lines[len(lines)-1])["trace.id"] != nil {
		t.Fatal("trace leaked into context-free call")
	}
}

type changingLogValuer struct {
	value slog.Value
	calls int
}

func (v *changingLogValuer) LogValue() slog.Value {
	v.calls++
	return v.value
}

func TestWrapHandlerResolvesBoundLogValuers(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		name := "root"
		if grouped {
			name = "nested_group"
		}
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			value := &changingLogValuer{value: slog.StringValue("original")}
			attrs := []slog.Attr{slog.Any("bound", value)}
			// A LogValuer may itself return a group containing more LogValuers.
			group := &changingLogValuer{value: slog.GroupValue(attrs...)}
			h := WrapHandler(slog.NewJSONHandler(&buf, nil), "svc", "", "", "")
			if grouped {
				h = h.WithGroup("request")
				attrs = []slog.Attr{slog.Group("outer", slog.Any("inner", group))}
			}
			logger := slog.New(h.WithAttrs(attrs))
			if value.calls != 1 || (grouped && group.calls != 1) {
				t.Fatalf("values not resolved at binding: leaf=%d group=%d", value.calls, group.calls)
			}
			// Binding must not resolve or overwrite caller-owned attribute slices.
			original := attrs[0].Value
			if grouped {
				original = original.Group()[0].Value
			}
			if original.Kind() != slog.KindLogValuer || group.value.Group()[0].Value.Kind() != slog.KindLogValuer {
				t.Fatal("binding modified caller-owned attributes")
			}
			value.value = slog.StringValue("changed")
			group.value = slog.StringValue("changed group")
			for i := 0; i < 2; i++ {
				logger.Info("message")
			}
			if value.calls != 1 || (grouped && group.calls != 1) {
				t.Fatalf("bound values evaluated again: leaf=%d group=%d", value.calls, group.calls)
			}
			for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
				rec := decodeLine(t, line)
				if grouped {
					rec = rec["request"].(map[string]any)["outer"].(map[string]any)["inner"].(map[string]any)
				}
				if rec["bound"] != "original" {
					t.Fatalf("bound value changed: %v", rec)
				}
			}
		})
	}
}
