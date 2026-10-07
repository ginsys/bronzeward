package monitor

import (
	"context"
	"fmt"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// stallIntervals is the number of intervals without progress after which the monitor is stalled,
// and between its repeated monitor-stalled alerts (dependency monitor §6.3, choice §11.8).
const stallIntervals = 3

// watch runs the watchdog once per interval until ctx ends, on a schedule of its own: a hung pass
// would never notice that it is stalled (§6.3).
func (m *Monitor) watch(ctx context.Context) {
	t := time.NewTicker(m.t.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		raised, err := m.watchdog(ctx)
		if err != nil && ctx.Err() == nil {
			m.report(ctx, "dependency monitor watchdog: %v", err)
		}
		if raised {
			m.logSoon(ctx)
		}
	}
}

// watchdog raises a monitor-stalled alert when one is due: when neither progress nor the last such
// alert is within three intervals of the database's time. It reads the DependencyMonitor row with
// a plain read, which waits for no lock; only a due alert locks the row, is decided again under
// the lock, and is recorded at clock_timestamp() read after it. A watchdog records no progress.
func (m *Monitor) watchdog(ctx context.Context) (bool, error) {
	stall := millis(stallIntervals * m.t.Interval)
	var due bool
	if err := m.db.QueryRowContext(ctx, `SELECT clock_timestamp() - GREATEST(progress, last_stalled) >= $1::interval
		FROM dependency_monitor`, stall).Scan(&due); err != nil {
		return false, fmt.Errorf("monitor row: %w", err)
	}
	if !due {
		return false, nil
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := m.holderTimeouts(ctx, tx); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM dependency_monitor FOR UPDATE`); err != nil {
		return false, fmt.Errorf("monitor row: %w", err)
	}
	var at time.Time
	if err := tx.QueryRowContext(ctx, `WITH c AS (SELECT clock_timestamp() AS at)
		SELECT c.at, c.at - GREATEST(progress, last_stalled) >= $1::interval FROM dependency_monitor, c`, stall).
		Scan(&at, &due); err != nil {
		return false, fmt.Errorf("monitor row: %w", err)
	}
	if !due {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO dependency_alert (id, kind, epoch, recorded_at)
		SELECT $1, 'monitor-stalled', epoch, $2 FROM installation_state`, id.New(id.DependencyAlert), at); err != nil {
		return false, fmt.Errorf("monitor-stalled alert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE dependency_monitor SET last_stalled = $1`, at); err != nil {
		return false, fmt.Errorf("monitor row: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
