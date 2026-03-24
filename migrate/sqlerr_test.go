package migrate

import (
	"errors"
	"strings"
	"testing"
)

func TestFormatSQLErrorWithToken(t *testing.T) {
	sql := "CREATE TABLE users (\n    id INTEGER PRIMARY KEY,\n    name TXET NOT NULL\n);\n"
	execErr := errors.New(`near "TXET": syntax error`)

	err := formatSQLError(sql, execErr, "failed to execute migration 1")

	msg := err.Error()
	if !strings.Contains(msg, "line 3") {
		t.Errorf("expected line 3 reference, got:\n%s", msg)
	}
	if !strings.Contains(msg, "> ") {
		t.Errorf("expected line marker, got:\n%s", msg)
	}
	if !strings.Contains(msg, "TXET") {
		t.Errorf("expected error token in output, got:\n%s", msg)
	}
}

func TestFormatSQLErrorWithCharOffset(t *testing.T) {
	sql := "SELECT 1;\nSELECT 2;\nBAD SQL HERE;\nSELECT 4;\n"
	// pgx-style: character offset 21 is in line 3 ("BAD SQL HERE;")
	execErr := errors.New(`ERROR: syntax error at or near "BAD" at character 21`)

	err := formatSQLError(sql, execErr, "test")

	msg := err.Error()
	if !strings.Contains(msg, "line 3") {
		t.Errorf("expected line 3, got:\n%s", msg)
	}
}

func TestFormatSQLErrorNoContext(t *testing.T) {
	sql := "SELECT 1;\n"
	execErr := errors.New("some generic database error")

	err := formatSQLError(sql, execErr, "test")

	msg := err.Error()
	// Should fall back to simple wrapping.
	if !strings.Contains(msg, "test: some generic database error") {
		t.Errorf("expected simple wrap, got:\n%s", msg)
	}
}

func TestFormatContext(t *testing.T) {
	lines := []string{"line1", "line2", "line3", "line4", "line5", "line6"}

	ctx := formatContext(lines, 3)

	if !strings.Contains(ctx, ">    3 | line3") {
		t.Errorf("expected marker on line 3, got:\n%s", ctx)
	}
	if !strings.Contains(ctx, "   1 | line1") {
		t.Errorf("expected line 1 in context, got:\n%s", ctx)
	}
	if !strings.Contains(ctx, "   5 | line5") {
		t.Errorf("expected line 5 in context, got:\n%s", ctx)
	}
}
