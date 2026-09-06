package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(context.Background(), []string{"telegram-companion", "wat"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr = %q, want unknown command", stderr.String())
	}
}

func TestRunMigrateUpRequiresPostgresDSN(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(context.Background(), []string{"telegram-companion", "migrate-up"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "POSTGRES_DSN is required") {
		t.Fatalf("stderr = %q, want POSTGRES_DSN error", stderr.String())
	}
}
