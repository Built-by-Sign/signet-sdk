package data

import (
    "context"
    "errors"
    "fmt"
    "log/slog"
    "net"
    "net/url"
    "os"

    "kratos-usage/internal/conf"

    "github.com/google/wire"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/pgx-contrib/pgxotel"
    "github.com/redis/go-redis/v9"
    "github.com/redis/go-redis/extra/redisotel/v9"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(NewData, NewGreeterRepo, NewValidationRepo)

// Data owns the PostgreSQL and Valkey clients.
type Data struct {
    postgres *pgxpool.Pool
    valkey   redis.UniversalClient
}

func requiredEnv(name string) (string, error) {
    value := os.Getenv(name)
    if value == "" {
        return "", fmt.Errorf("%s is required", name)
    }
    return value, nil
}

func postgresURL() (string, error) {
    host, err := requiredEnv("PGHOST")
    if err != nil { return "", err }
    port, err := requiredEnv("PGPORT")
    if err != nil { return "", err }
    database, err := requiredEnv("PGDATABASE")
    if err != nil { return "", err }
    username, err := requiredEnv("PGUSER")
    if err != nil { return "", err }
    password, err := requiredEnv("PGPASSWORD")
    if err != nil { return "", err }
    return (&url.URL{
        Scheme: "postgres",
        User: url.UserPassword(username, password),
        Host: net.JoinHostPort(host, port),
        Path: database,
        RawQuery: "sslmode=" + url.QueryEscape(envOr("PGSSLMODE", "disable")),
    }).String(), nil
}

func envOr(name, fallback string) string {
    if value := os.Getenv(name); value != "" { return value }
    return fallback
}

// NewData creates instrumented dependency clients. Credentials are read only from runtime environment.
func NewData(_ *conf.Data) (*Data, func(), error) {
    connectionURL, err := postgresURL()
    if err != nil { return nil, nil, err }
    poolConfig, err := pgxpool.ParseConfig(connectionURL)
    if err != nil { return nil, nil, fmt.Errorf("parse PostgreSQL config: %w", err) }
    poolConfig.ConnConfig.Tracer = &pgxotel.QueryTracer{Name: "signet.postgresql"}
    postgres, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
    if err != nil { return nil, nil, fmt.Errorf("create PostgreSQL pool: %w", err) }

    valkeyHost, err := requiredEnv("VALKEY_HOST")
    if err != nil { postgres.Close(); return nil, nil, err }
    valkeyPort, err := requiredEnv("VALKEY_PORT")
    if err != nil { postgres.Close(); return nil, nil, err }
    valkey := redis.NewClient(&redis.Options{
        Addr: net.JoinHostPort(valkeyHost, valkeyPort),
        Username: os.Getenv("VALKEY_USERNAME"),
        Password: mustEnv("VALKEY_PASSWORD"),
    })
    if err := errors.Join(redisotel.InstrumentTracing(valkey), redisotel.InstrumentMetrics(valkey)); err != nil {
        _ = valkey.Close()
        postgres.Close()
        return nil, nil, fmt.Errorf("instrument Valkey client: %w", err)
    }

    cleanup := func() {
        _ = valkey.Close()
        postgres.Close()
        slog.Info("closing the data resources")
    }
    return &Data{postgres: postgres, valkey: valkey}, cleanup, nil
}

func mustEnv(name string) string { return os.Getenv(name) }