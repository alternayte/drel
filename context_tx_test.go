package drel

import (
	"context"
	"testing"
)

func TestFromContext_Empty(t *testing.T) {
	if tx, ok := FromContext(context.Background()); ok || tx != nil {
		t.Fatalf("FromContext on a bare context returned (%v, %v), want (nil, false)", tx, ok)
	}
}

func TestMustFromContext_Panics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustFromContext did not panic without a transaction in the context")
		}
	}()
	MustFromContext(context.Background())
}

func TestFromContext_ReturnsStoredTx(t *testing.T) {
	want := &Tx{}
	ctx := contextWithTx(context.Background(), want)

	got, ok := FromContext(ctx)
	if !ok || got != want {
		t.Fatalf("FromContext returned (%v, %v), want (%v, true)", got, ok, want)
	}
	if MustFromContext(ctx) != want {
		t.Fatal("MustFromContext returned a different transaction")
	}
}
