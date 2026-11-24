package data

import (
    "context"
    "errors"
    "fmt"
    "time"

    "kratos-usage/internal/biz"
)

var ErrControlledValidation = errors.New("controlled validation error")

type validationRepo struct {
    postgres func(context.Context) error
    valkey   func(context.Context) error
}

func newValidationRepo(postgres func(context.Context) error, valkey func(context.Context) error) *validationRepo {
    return &validationRepo{postgres: postgres, valkey: valkey}
}

// NewValidationRepo wires the instrumented PostgreSQL and Valkey probes into the business interface.
func NewValidationRepo(data *Data) biz.ValidationRepo {
    return newValidationRepo(
        func(ctx context.Context) error {
            var value int
            if err := data.postgres.QueryRow(ctx, "SELECT 1").Scan(&value); err != nil {
                return fmt.Errorf("PostgreSQL validation query: %w", err)
            }
            if value != 1 { return errors.New("PostgreSQL validation query returned unexpected value") }
            return nil
        },
        func(ctx context.Context) error {
            const key = "signet_otel_validation:probe"
            if err := data.valkey.Set(ctx, key, "ok", time.Minute).Err(); err != nil {
                return fmt.Errorf("Valkey validation set: %w", err)
            }
            value, err := data.valkey.Get(ctx, key).Result()
            if err != nil { return fmt.Errorf("Valkey validation get: %w", err) }
            if value != "ok" { return errors.New("Valkey validation read returned unexpected value") }
            return nil
        },
    )
}

func (r *validationRepo) Execute(ctx context.Context, mode string) error {
    switch mode {
    case "error":
        return ErrControlledValidation
    case "timeout":
        <-ctx.Done()
        return ctx.Err()
    case "success":
        if r.postgres != nil {
            if err := r.postgres(ctx); err != nil { return err }
        }
        if r.valkey != nil { return r.valkey(ctx) }
        return nil
    default:
        return errors.New("unsupported validation mode")
    }
}