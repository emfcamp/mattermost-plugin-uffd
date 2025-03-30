package ctxlog

import (
	"context"

	log "github.com/sirupsen/logrus"
)

type contextKeyStruct struct{}

var contextKey = contextKeyStruct{}

func FromContext(ctx context.Context) log.FieldLogger {
	l, ok := ctx.Value(contextKey).(log.FieldLogger)
	if !ok {
		return log.NewEntry(log.StandardLogger()).WithContext(ctx)
	}
	return l
}

func NewContext(ctx context.Context, l log.FieldLogger) context.Context {
	return context.WithValue(ctx, contextKey, l)
}
