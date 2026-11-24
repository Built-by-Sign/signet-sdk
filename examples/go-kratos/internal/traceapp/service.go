package traceapp

import (
	"context"
	"fmt"
	"log/slog"

	v1 "kratos-usage/api/trace/v1"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// ValidationService is service A. It runs A locally, then calls B using the
// same transport (HTTP or gRPC) used by the incoming request.
type ValidationService struct {
	v1.UnimplementedValidationServer
	httpWorker v1.WorkerHTTPClient
	grpcWorker v1.WorkerClient
	tracer     trace.Tracer
}

func NewValidationService(httpWorker v1.WorkerHTTPClient, grpcWorker v1.WorkerClient) *ValidationService {
	return &ValidationService{
		httpWorker: httpWorker,
		grpcWorker: grpcWorker,
		tracer:     otel.Tracer("signet.example.service-a"),
	}
}

func (s *ValidationService) RunChain(ctx context.Context, req *v1.RunChainRequest) (*v1.TraceReply, error) {
	slog.InfoContext(ctx, "service A chain started", "a.outcome", req.GetA().String(), "b.outcome", req.GetB().String())
	requestSpan := trace.SpanFromContext(ctx)
	requestSpan.SetAttributes(
		attribute.String("test.a.outcome", req.GetA().String()),
		attribute.String("test.b.outcome", req.GetB().String()),
		attribute.String("test.worker.called", "false"),
	)
	if err := validateOutcome("a", req.GetA()); err != nil {
		return nil, err
	}
	if err := s.executeA(ctx, req.GetA()); err != nil {
		return nil, err
	}
	if err := validateOutcome("b", req.GetB()); err != nil {
		return nil, err
	}

	requestSpan.SetAttributes(attribute.String("test.worker.called", "true"))
	workerReq := &v1.ExecuteRequest{Outcome: req.GetB()}
	tr, ok := transport.FromServerContext(ctx)
	if !ok {
		return nil, errors.InternalServer("TRANSPORT_MISSING", "server transport is missing")
	}
	switch tr.Kind() {
	case transport.KindHTTP:
		_, err := s.httpWorker.Execute(ctx, workerReq)
		if err != nil {
			return nil, fmt.Errorf("service b over HTTP: %w", err)
		}
	case transport.KindGRPC:
		_, err := s.grpcWorker.Execute(ctx, workerReq)
		if err != nil {
			return nil, fmt.Errorf("service b over gRPC: %w", err)
		}
	default:
		return nil, errors.InternalServer("TRANSPORT_UNSUPPORTED", "unsupported server transport")
	}

	return traceReply(ctx, req.GetA(), req.GetB()), nil
}

func (s *ValidationService) RunLocal(ctx context.Context, req *v1.RunLocalRequest) (*v1.TraceReply, error) {
	slog.InfoContext(ctx, "service A local started", "a.outcome", req.GetA().String())
	if err := validateOutcome("a", req.GetA()); err != nil {
		return nil, err
	}
	if err := s.executeA(ctx, req.GetA()); err != nil {
		return nil, err
	}
	return traceReply(ctx, req.GetA(), v1.Outcome_OUTCOME_UNSPECIFIED), nil
}

func (s *ValidationService) executeA(ctx context.Context, outcome v1.Outcome) error {
	_, span := s.tracer.Start(ctx, "service_a.execute")
	defer span.End()
	span.SetAttributes(attribute.String("test.outcome", outcome.String()))
	if outcome == v1.Outcome_OUTCOME_ERROR {
		err := errors.InternalServer("SERVICE_A_ERROR", "controlled service A error")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// WorkerService is service B.
type WorkerService struct {
	v1.UnimplementedWorkerServer
	tracer trace.Tracer
}

func NewWorkerService() *WorkerService {
	return &WorkerService{tracer: otel.Tracer("signet.example.service-b")}
}

func (s *WorkerService) Execute(ctx context.Context, req *v1.ExecuteRequest) (*v1.ExecuteReply, error) {
	slog.InfoContext(ctx, "service B execute started", "b.outcome", req.GetOutcome().String())
	if err := validateOutcome("outcome", req.GetOutcome()); err != nil {
		return nil, err
	}
	_, span := s.tracer.Start(ctx, "service_b.execute")
	defer span.End()
	span.SetAttributes(attribute.String("test.outcome", req.GetOutcome().String()))
	if req.GetOutcome() == v1.Outcome_OUTCOME_ERROR {
		err := errors.InternalServer("SERVICE_B_ERROR", "controlled service B error")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return &v1.ExecuteReply{Outcome: req.GetOutcome()}, nil
}

func validateOutcome(field string, outcome v1.Outcome) error {
	if outcome != v1.Outcome_OUTCOME_OK && outcome != v1.Outcome_OUTCOME_ERROR {
		return errors.BadRequest("INVALID_OUTCOME", field+" must be OUTCOME_OK or OUTCOME_ERROR")
	}
	return nil
}

func traceReply(ctx context.Context, a, b v1.Outcome) *v1.TraceReply {
	return &v1.TraceReply{
		TraceId: trace.SpanContextFromContext(ctx).TraceID().String(),
		A:       a,
		B:       b,
	}
}
