package kiro

import (
	"context"
	"encoding/json"
	"log/slog"
)

const adapterDebugLogLimit = 64 * 1024

func debugJSON(ctx context.Context, msg string, value any, attrs ...any) {
	logger := slog.Default()
	if logger == nil || !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}

	data, err := json.Marshal(value)
	if err != nil {
		attrs = append(attrs, "marshal_error", err.Error())
		logger.DebugContext(ctx, msg, attrs...)
		return
	}
	if len(data) > adapterDebugLogLimit {
		data = append(data[:adapterDebugLogLimit], []byte("...(truncated)")...)
	}
	attrs = append(attrs, "payload", string(data))
	logger.DebugContext(ctx, msg, attrs...)
}
