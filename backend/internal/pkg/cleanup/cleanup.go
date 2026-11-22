// Package cleanup preserves errors from deferred resource release.
package cleanup

import (
	"database/sql"
	"errors"
	"io"
)

func Close(result *error, closer io.Closer) {
	if err := closer.Close(); err != nil {
		*result = errors.Join(*result, err)
	}
}

// Rollback accepts both Ent and database/sql transactions. A transaction
// already committed or rolled back by its caller needs no further cleanup.
func Rollback(result *error, tx interface{ Rollback() error }) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		*result = errors.Join(*result, err)
	}
}
