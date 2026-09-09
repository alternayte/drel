package drel_test

import (
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alternayte/drel/internal/codegen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompositeKey_GeneratedFilesAreCurrent proves the checked-in generated
// files of the composite-key test models are the emitter's current output. The
// integration tests exercise those files, so a stale file would prove nothing
// about the emitter. The test needs no database, so it must stay outside the
// integration tag: the fast suite is where a stale file has to be caught.
func TestCompositeKey_GeneratedFilesAreCurrent(t *testing.T) {
	models, err := codegen.ScanPackages([]string{"./internal/testmodels"}, ".")
	require.NoError(t, err, "scan the composite-key test models")

	seen := 0
	for _, m := range models {
		if !strings.Contains(m.Name, "OrderLine") && m.Name != "TenantDoc" {
			continue
		}
		seen++
		require.Len(t, m.Key, 2, "model %s must have a two-column key", m.Name)
		src, err := codegen.EmitModelFileChecked(m)
		require.NoError(t, err)
		want, err := format.Source([]byte(src))
		require.NoError(t, err)
		path := filepath.Join("internal", "testmodels", strings.ToLower(m.Name)+"_drel.go")
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, string(want), string(got), "%s is stale; regenerate it", path)
	}
	assert.Equal(t, 5, seen, "every composite-key test model must be scanned")
}
