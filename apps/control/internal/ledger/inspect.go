package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
)

// ReadRawFrame opens the ledger read-only for explicit offline inspection.
// The ownership lock requires the central server to be stopped, so this path
// cannot race live writes or become a second operational data owner.
func ReadRawFrame(ctx context.Context, home, runID string, sequence int) ([]byte, error) {
	if sequence <= 0 || runID == "" {
		return nil, fmt.Errorf("run id and positive frame sequence are required")
	}
	home = filepath.Clean(home)
	lock, err := acquireOwnershipLock(home)
	if err != nil {
		return nil, fmt.Errorf("offline raw-frame inspection requires the central server to be stopped: %w", err)
	}
	defer releaseOwnershipLock(lock)

	dbPath := filepath.Join(home, DatabaseFilename)
	u := &url.URL{Scheme: "file", Path: dbPath}
	query := u.Query()
	query.Set("mode", "ro")
	u.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open ledger for inspection: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("open ledger for inspection: %w", err)
	}
	applied, err := readAppliedMigrations(ctx, db)
	if err != nil {
		return nil, err
	}
	if err := validateAppliedMigrations(applied, migrations()); err != nil {
		return nil, err
	}
	if err := integrityCheck(ctx, db); err != nil {
		return nil, fmt.Errorf("inspect ledger integrity: %w", err)
	}
	var raw []byte
	err = db.QueryRowContext(ctx, `SELECT raw_payload FROM vendor_frames WHERE run_id = ? AND frame_sequence = ?`, runID, sequence).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read raw vendor frame: %w", err)
	}
	return raw, nil
}
