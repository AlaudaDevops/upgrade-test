package context

import (
	"context"

	"go.uber.org/zap"
)

type loggerKey struct{}

// WithLogger returns a new context with the given logger
func WithLogger(ctx context.Context, logger *zap.SugaredLogger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

// LoggerFromContext returns the logger from the context
func LoggerFromContext(ctx context.Context) *zap.SugaredLogger {
	return ctx.Value(loggerKey{}).(*zap.SugaredLogger)
}

type maxRetriesKey struct{}

func WithMaxRetries(ctx context.Context, maxRetries int) context.Context {
	return context.WithValue(ctx, maxRetriesKey{}, maxRetries)
}

func MaxRetriesFromContext(ctx context.Context) int {
	return ctx.Value(maxRetriesKey{}).(int)
}

type operatorNamespaceKey struct{}

func WithOperatorNamespace(ctx context.Context, operatorNamespace string) context.Context {
	return context.WithValue(ctx, operatorNamespaceKey{}, operatorNamespace)
}

func OperatorNamespaceFromContext(ctx context.Context) string {
	return ctx.Value(operatorNamespaceKey{}).(string)
}

type systemNamespaceKey struct{}

func WithSystemNamespace(ctx context.Context, systemNamespace string) context.Context {
	return context.WithValue(ctx, systemNamespaceKey{}, systemNamespace)
}

func SystemNamespaceFromContext(ctx context.Context) string {
	return ctx.Value(systemNamespaceKey{}).(string)
}
