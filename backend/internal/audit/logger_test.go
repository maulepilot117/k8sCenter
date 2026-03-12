package audit

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestSlogLogger_Log(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	al := NewSlogLogger(logger)

	err := al.Log(context.Background(), Entry{
		Timestamp:         time.Now(),
		ClusterID:         "local",
		User:              "admin",
		SourceIP:          "127.0.0.1",
		Action:            ActionCreate,
		ResourceKind:      "deployment",
		ResourceNamespace: "default",
		ResourceName:      "nginx",
		Result:            ResultSuccess,
	})

	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}
}

func TestSlogLogger_Implements_Logger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	var _ Logger = NewSlogLogger(logger)
}
