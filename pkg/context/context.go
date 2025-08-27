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
