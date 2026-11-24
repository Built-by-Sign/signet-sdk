package biz

import (
    "context"
    "log/slog"
    "time"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/codes"
    "go.opentelemetry.io/otel/trace"
)

// ValidationRepo executes the controlled PostgreSQL and Valkey workflow.
type ValidationRepo interface {
    Execute(context.Context, string) error
}

// DependencyWorker calls the downstream Go service.
type DependencyWorker interface {
    Call(context.Context, string) error
}

// ValidationUsecase runs a bounded asynchronous dependency workflow and a downstream call.
type ValidationUsecase struct {
    repo    ValidationRepo
    worker  DependencyWorker
    timeout time.Duration
    tracer  trace.Tracer
}

func NewValidationUsecase(repo ValidationRepo, worker DependencyWorker, timeout time.Duration) *ValidationUsecase {
    return &ValidationUsecase{repo: repo, worker: worker, timeout: timeout, tracer: otel.Tracer("kratos-usage.validation")}
}

func (uc *ValidationUsecase) Execute(ctx context.Context, mode string) error {
    ctx, cancel := context.WithTimeout(ctx, uc.timeout)
    defer cancel()

    done := make(chan error, 1)
    go func() {
        childCtx, span := uc.tracer.Start(ctx, "async.validation", trace.WithSpanKind(trace.SpanKindInternal))
        defer span.End()
        slog.InfoContext(childCtx, "validation async started",
            "event.name", "validation.async.started",
            "validation.mode", mode,
        )
        err := uc.repo.Execute(childCtx, mode)
        if err != nil {
            span.RecordError(err)
            span.SetStatus(codes.Error, err.Error())
            slog.ErrorContext(childCtx, "validation async failed",
                "event.name", "validation.async.failed",
                "validation.mode", mode,
                "error.type", "dependency_error",
            )
        } else {
            slog.InfoContext(childCtx, "validation async completed",
                "event.name", "validation.async.completed",
                "validation.mode", mode,
            )
        }
        done <- err
    }()

    select {
    case err := <-done:
        if err != nil { return err }
    case <-ctx.Done():
        return ctx.Err()
    }

    if err := uc.worker.Call(ctx, mode); err != nil { return err }
    return nil
}
