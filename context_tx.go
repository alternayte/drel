package drel

import (
	"context"
	"errors"
)

// txCtxKey is the private context key that carries the active transaction.
type txCtxKey struct{}

// ErrNestedTxOptions reports that a nested WithTx call carried transaction
// options. Isolation and read-only belong to the outermost call, because a
// savepoint cannot change them.
var ErrNestedTxOptions = errors.New("drel: transaction options are not allowed on a nested WithTx call")

// contextWithTx returns a copy of ctx that carries tx.
func contextWithTx(ctx context.Context, tx *Tx) context.Context {
	return context.WithValue(ctx, txCtxKey{}, tx)
}

// FromContext returns the transaction that WithTx put in ctx. The second result
// is false when no transaction is present.
func FromContext(ctx context.Context) (*Tx, bool) {
	tx, ok := ctx.Value(txCtxKey{}).(*Tx)
	return tx, ok
}

// MustFromContext returns the transaction that WithTx put in ctx. It panics when
// no transaction is present. Use it in code that a transaction must always
// surround, so that a wiring fault fails at once.
func MustFromContext(ctx context.Context) *Tx {
	tx, ok := FromContext(ctx)
	if !ok {
		panic("drel: no transaction in context; wrap the call in Engine.WithTx")
	}
	return tx
}

// WithTx runs fn inside a database transaction and puts that transaction in the
// context it passes to fn. Retrieve it with FromContext or MustFromContext.
//
// The transaction commits when fn returns nil. It rolls back when fn returns an
// error or panics.
//
// A nested WithTx call on the same engine reuses the transaction from the
// context and opens a savepoint. It does not begin a second transaction, and it
// does not commit. An error from the nested call rolls back to the savepoint
// only, and the outer call still decides the commit. A nested call returns
// ErrNestedTxOptions if it carries transaction options.
//
// The behaviour is the same for Postgres, SQLite and LibSQL.
func (e *Engine) WithTx(ctx context.Context, fn func(ctx context.Context) error, opts ...TxOption) error {
	if outer, ok := FromContext(ctx); ok && outer.engine == e {
		if len(opts) > 0 {
			return ErrNestedTxOptions
		}
		return outer.Savepoint(ctx, "withtx", func(sp *Tx) error {
			return fn(contextWithTx(ctx, sp))
		})
	}
	return e.Transaction(ctx, func(tx *Tx) error {
		return fn(contextWithTx(ctx, tx))
	}, opts...)
}
