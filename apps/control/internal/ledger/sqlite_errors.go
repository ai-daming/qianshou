package ledger

import (
	"errors"
	"fmt"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func classifySQLiteWriteError(err error, operation, conflictMessage string) error {
	if isSQLiteConstraint(err) {
		return fmt.Errorf("%s: %w: %w", conflictMessage, ErrConflict, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func isSQLiteConstraint(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqlite3.SQLITE_CONSTRAINT
}
