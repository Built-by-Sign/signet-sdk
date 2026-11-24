package biz

import (
    "context"
    "errors"
    "testing"
    "time"
)

type fakeValidationRepo struct {
    mode string
    execute func(context.Context, string) error
}

func (r *fakeValidationRepo) Execute(ctx context.Context, mode string) error {
    r.mode = mode
    return r.execute(ctx, mode)
}

type fakeDependencyWorker struct {
    mode string
    call func(context.Context, string) error
}

func (w *fakeDependencyWorker) Call(ctx context.Context, mode string) error {
    w.mode = mode
    return w.call(ctx, mode)
}

func TestValidationUsecaseExecutesSuccessWithDerivedContext(t *testing.T) {
    repo := &fakeValidationRepo{execute: func(ctx context.Context, mode string) error {
        if _, ok := ctx.Deadline(); !ok { t.Fatal("expected derived context deadline") }
        if mode != "success" { t.Fatalf("mode = %q, want success", mode) }
        return nil
    }}
    worker := &fakeDependencyWorker{call: func(context.Context, string) error { return nil }}

    err := NewValidationUsecase(repo, worker, 100*time.Millisecond).Execute(context.Background(), "success")
    if err != nil { t.Fatalf("Execute() error = %v", err) }
    if repo.mode != "success" || worker.mode != "success" {
        t.Fatalf("modes = repo:%q worker:%q, want success", repo.mode, worker.mode)
    }
}

func TestValidationUsecasePropagatesRepositoryError(t *testing.T) {
    want := errors.New("controlled dependency failure")
    repo := &fakeValidationRepo{execute: func(context.Context, string) error { return want }}
    worker := &fakeDependencyWorker{call: func(context.Context, string) error { t.Fatal("worker must not run after repository failure"); return nil }}

    err := NewValidationUsecase(repo, worker, 100*time.Millisecond).Execute(context.Background(), "error")
    if !errors.Is(err, want) { t.Fatalf("Execute() error = %v, want %v", err, want) }
}

func TestValidationUsecasePropagatesWorkerError(t *testing.T) {
    want := errors.New("worker failure")
    repo := &fakeValidationRepo{execute: func(context.Context, string) error { return nil }}
    worker := &fakeDependencyWorker{call: func(context.Context, string) error { return want }}

    err := NewValidationUsecase(repo, worker, 100*time.Millisecond).Execute(context.Background(), "error")
    if !errors.Is(err, want) { t.Fatalf("Execute() error = %v, want %v", err, want) }
}

func TestValidationUsecaseReturnsDeadlineExceeded(t *testing.T) {
    repo := &fakeValidationRepo{execute: func(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() }}
    worker := &fakeDependencyWorker{call: func(context.Context, string) error { return nil }}

    err := NewValidationUsecase(repo, worker, 10*time.Millisecond).Execute(context.Background(), "timeout")
    if !errors.Is(err, context.DeadlineExceeded) { t.Fatalf("Execute() error = %v, want deadline exceeded", err) }
}