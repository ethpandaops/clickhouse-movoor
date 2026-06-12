package tiering

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// DateTime64(6) columns compared against bare `?` parameters silently truncate
// the bound time.Time to whole seconds, letting events from the cutoff's own
// second leak across it. This class of bug recurred three times in one day —
// every microsecond-column predicate must go through toDateTime64(?, 6) with
// dateTime64MicrosParam. SQL lives in string literals where the type system
// cannot see, so the invariant is enforced by scanning the source.
func TestMicrosecondPredicatesUseExplicitPrecision(t *testing.T) {
	bare := regexp.MustCompile(`_microseconds\s*(>=|>|<=|<)\s*\?`)

	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, readErr := os.ReadFile(file)
		require.NoError(t, readErr)
		for i, line := range strings.Split(string(source), "\n") {
			require.Falsef(t, bare.MatchString(line),
				"%s:%d compares a *_microseconds column against a bare ? — bind via toDateTime64(?, 6) with dateTime64MicrosParam instead", file, i+1)
		}
	}
}
