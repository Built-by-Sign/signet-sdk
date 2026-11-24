package biz

import (
	"context"
	"log/slog"

	v1 "kratos-usage/api/helloworld/v1"

	"github.com/built-by-sign/signet/sdk/observable"

	"github.com/go-kratos/kratos/v3/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

var (
	// ErrUserNotFound is user not found.
	ErrUserNotFound = errors.NotFound(v1.ErrorReason_USER_NOT_FOUND.String(), "user not found")
)

// Greeter is a Greeter model.
type Greeter struct {
	Hello string
}

// GreeterRepo is a Greater repo.
type GreeterRepo interface {
	Save(context.Context, *Greeter) (*Greeter, error)
	Update(context.Context, *Greeter) (*Greeter, error)
	FindByID(context.Context, int64) (*Greeter, error)
	ListByHello(context.Context, string) ([]*Greeter, error)
	ListAll(context.Context) ([]*Greeter, error)
}

// GreeterUsecase is a Greeter usecase.
type GreeterUsecase struct {
	repo    GreeterRepo
	greetOp *observable.Op
}

// NewGreeterUsecase new a Greeter usecase.
func NewGreeterUsecase(repo GreeterRepo) *GreeterUsecase {
	meter := otel.Meter("kratos-usage.biz")
	tracer := otel.Tracer("kratos-usage.biz")
	return &GreeterUsecase{
		repo:    repo,
		greetOp: observable.NewOp(meter, tracer, "biz.greet"),
	}
}

// CreateGreeter creates a Greeter, and returns the new Greeter.
// Observable decorator auto-records: biz.greet_total + biz.greet_seconds + trace span.
// Use name "error" to trigger ErrUserNotFound for testing error metrics.
func (uc *GreeterUsecase) CreateGreeter(ctx context.Context, g *Greeter) (*Greeter, error) {
	return observable.ObserveVal(uc.greetOp, ctx, "GreeterUsecase.CreateGreeter",
		[]attribute.KeyValue{attribute.String("name", g.Hello)},
		func(ctx context.Context) (*Greeter, error) {
			slog.InfoContext(ctx, "CreateGreeter", "hello", g.Hello)
			if g.Hello == "error" {
				return nil, ErrUserNotFound
			}
			return uc.repo.Save(ctx, g)
		},
	)
}
