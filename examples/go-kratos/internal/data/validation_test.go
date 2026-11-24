package data

import (
    "context"
    "errors"
    "testing"
)

func TestValidationRepoRunsPostgresAndValkeyForSuccess(t *testing.T) {
    calls := []string{}
    repo := newValidationRepo(
        func(context.Context) error { calls = append(calls, "postgres"); return nil },
        func(context.Context) error { calls = append(calls, "valkey"); return nil },
    )

    if err := repo.Execute(context.Background(), "success"); err != nil {
        t.Fatalf("Execute() error = %v", err)
    }
    if len(calls) != 2 || calls[0] != "postgres" || calls[1] != "valkey" {
        t.Fatalf("calls = %v, want [postgres valkey]", calls)
    }
}

func TestValidationRepoReturnsControlledError(t *testing.T) {
    repo := newValidationRepo(
        func(context.Context) error { return errors.New("postgres must not run") },
        func(context.Context) error { return errors.New("valkey must not run") },
    )

    err := repo.Execute(context.Background(), "error")
    if !errors.Is(err, ErrControlledValidation) {
        t.Fatalf("Execute() error = %v, want controlled validation error", err)
    }
}

func TestValidationRepoWaitsForContextInTimeoutMode(t *testing.T) {
    repo := newValidationRepo(nil, nil)
    ctx, cancel := context.WithCancel(context.Background())
    cancel()

    err := repo.Execute(ctx, "timeout")
    if !errors.Is(err, context.Canceled) {
        t.Fatalf("Execute() error = %v, want context canceled", err)
    }
}