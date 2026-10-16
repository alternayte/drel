package pgxdriver

import (
	"github.com/alternayte/drel/internal/driver"
)

// Compile-time assertions: the pgx pool driver and its transactions both
// implement the optional BulkCopier / TxBulkCopier capabilities.
var (
	_ driver.BulkCopier   = (*PgxDriver)(nil)
	_ driver.TxBulkCopier = (*pgxTx)(nil)
	_ driver.TxUnderlyer  = (*pgxTx)(nil)
)
