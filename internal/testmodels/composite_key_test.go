package testmodels_test

import (
	"testing"

	"github.com/alternayte/drel/internal/testmodels"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompositeNormalizeKey_DriverValues covers the []any branch of the
// generated NormalizeKey. Nothing else in the repository reaches it, because a
// composite-key model cannot be the target of a relationship. The values are
// the shapes a driver returns: int64 for an integer column, and the canonical
// string or the raw bytes for a uuid column.
func TestCompositeNormalizeKey_DriverValues(t *testing.T) {
	require.NotNil(t, testmodels.OrderLineMeta.NormalizeKey)

	got := testmodels.OrderLineMeta.NormalizeKey([]any{int64(3), int64(7)})
	assert.Equal(t, testmodels.OrderLineKey{OrderID: 3, LineNo: 7}, got,
		"an int64 driver value must convert to the declared named key type")

	key, ok := got.(testmodels.OrderLineKey)
	require.True(t, ok)
	assert.IsType(t, testmodels.LineNo(0), key.LineNo)
}

func TestCompositeNormalizeKey_UUIDColumn(t *testing.T) {
	id := uuid.MustParse("018f3f1a-0000-7000-8000-0000000000aa")
	want := testmodels.TenantDocKey{TenantID: id, DocNo: 4}

	assert.Equal(t, want, testmodels.TenantDocMeta.NormalizeKey([]any{id.String(), int64(4)}),
		"a uuid returned as a string must convert to uuid.UUID")
	assert.Equal(t, want, testmodels.TenantDocMeta.NormalizeKey([]any{[16]byte(id), int64(4)}),
		"a uuid returned as raw bytes must convert to uuid.UUID")
}

// TestCompositeNormalizeKey_WrongShape proves the generated normalizer returns
// its input untouched when the shape does not match, rather than panicking.
func TestCompositeNormalizeKey_WrongShape(t *testing.T) {
	assert.Equal(t, 5, testmodels.OrderLineMeta.NormalizeKey(5))
	assert.Equal(t, []any{int64(1)}, testmodels.OrderLineMeta.NormalizeKey([]any{int64(1)}))
}

// TestCompositeKeyValues proves the generated splitter yields one value per key
// column, in key order.
func TestCompositeKeyValues(t *testing.T) {
	vals := testmodels.OrderLineMeta.KeyValues(testmodels.OrderLineKey{OrderID: 3, LineNo: 7})
	assert.Equal(t, []any{3, testmodels.LineNo(7)}, vals)
	assert.Equal(t, []string{"order_id", "line_no"}, testmodels.OrderLineMeta.PKColumns)
}
