package migrate

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Common patterns for extracting position from driver error messages.
var (
	// pgx: `at character 123`
	pgPositionRe = regexp.MustCompile(`at character (\d+)`)
	// Both pgx and sqlite: `near "token"` or `at or near "token"`
	nearTokenRe = regexp.MustCompile(`(?:near|at or near) "([^"]+)"`)
)

// formatSQLError wraps a SQL execution error with source context showing
// the line where the error likely occurred.
func formatSQLError(sql string, execErr error, label string) error {
	msg := execErr.Error()
	lines := strings.Split(sql, "\n")

	// Try to find the error position by character offset.
	if line, ok := lineFromCharOffset(msg, lines); ok {
		return fmt.Errorf("%s (line %d):\n%s\n\n%w",
			label, line, formatContext(lines, line), execErr)
	}

	// Fall back to searching for the token mentioned in the error.
	if m := nearTokenRe.FindStringSubmatch(msg); m != nil {
		token := m[1]
		for i, l := range lines {
			if strings.Contains(l, token) {
				line := i + 1
				return fmt.Errorf("%s (line %d):\n%s\n\n%w",
					label, line, formatContext(lines, line), execErr)
			}
		}
	}

	// No position info found — return the original error.
	return fmt.Errorf("%s: %w", label, execErr)
}

// lineFromCharOffset extracts a character offset from the error message
// and converts it to a 1-based line number.
func lineFromCharOffset(msg string, lines []string) (int, bool) {
	var offset int
	var found bool

	if m := pgPositionRe.FindStringSubmatch(msg); m != nil {
		offset, _ = strconv.Atoi(m[1])
		found = true
	}

	if !found || offset <= 0 {
		return 0, false
	}

	// Convert character offset to line number.
	pos := 0
	for i, line := range lines {
		pos += len(line) + 1 // +1 for newline
		if pos >= offset {
			return i + 1, true
		}
	}

	return len(lines), true
}

// formatContext returns a few lines of SQL around the given line number,
// with line numbers and a marker on the error line.
func formatContext(lines []string, errorLine int) string {
	const radius = 2
	start := errorLine - radius - 1
	if start < 0 {
		start = 0
	}
	end := errorLine + radius
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		marker := "  "
		if i+1 == errorLine {
			marker = "> "
		}
		fmt.Fprintf(&b, "%s%4d | %s\n", marker, i+1, lines[i])
	}
	return b.String()
}
