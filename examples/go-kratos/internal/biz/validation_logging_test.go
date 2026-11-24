package biz

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type loggingValidationRepo struct{}

func (loggingValidationRepo) Execute(context.Context, string) error { return nil }

type loggingDependencyWorker struct{}

func (loggingDependencyWorker) Call(context.Context, string) error { return nil }

func TestValidationUsecaseLogsAsyncLifecycle(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	usecase := NewValidationUsecase(loggingValidationRepo{}, loggingDependencyWorker{}, time.Second)
	if err := usecase.Execute(context.Background(), "success"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	logs := output.String()
	for _, expected := range []string{
		`"event.name":"validation.async.started"`,
		`"event.name":"validation.async.completed"`,
		`"validation.mode":"success"`,
	} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("expected log field %s in %s", expected, logs)
		}
	}
}
