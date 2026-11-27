//go:build postgres

package postgres

import (
	"context"
	"strconv"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type observationProducers struct{ *runner }

func (t *tx) ObservationProducers() store.ObservationProducers { return observationProducers{t.r} }

func (r observationProducers) LockMachine(ctx context.Context, machine int64) error {
	var id int64
	err := r.QueryRow(ctx, `SELECT id FROM machines WHERE id=$1 AND removed_at IS NULL FOR NO KEY UPDATE`, machine).Scan(&id)
	return notFound(err)
}

func (r observationProducers) Get(ctx context.Context, machine int64, producer string) (store.ObservationProducer, error) {
	var state store.ObservationProducer
	err := r.QueryRow(ctx, `SELECT active,cursor FROM observation_producers WHERE machine_id=$1 AND producer=$2`, machine, producer).Scan(&state.Active, &state.Cursor)
	return state, notFound(err)
}

func (r observationProducers) Replace(ctx context.Context, machine int64, producer string) error {
	if _, err := r.Exec(ctx, `UPDATE observation_producers SET active=false WHERE machine_id=$1 AND active`, machine); err != nil {
		return err
	}
	_, err := r.Exec(ctx, `INSERT INTO observation_producers (machine_id,producer) VALUES ($1,$2)`, machine, producer)
	return err
}

func (r observationProducers) SetCursor(ctx context.Context, machine int64, producer string, cursor int64) error {
	_, err := r.Exec(ctx, `UPDATE observation_producers SET cursor=$3 WHERE machine_id=$1 AND producer=$2`, machine, producer, cursor)
	return err
}

func (r observationProducers) RecordDisposition(ctx context.Context, machine int64, producer string, cursor int64, d store.ObservationDisposition) error {
	var from, to *time.Time
	if !d.From.IsZero() {
		from = &d.From
	}
	if !d.To.IsZero() {
		to = &d.To
	}
	_, err := r.Exec(ctx, `INSERT INTO observation_dispositions (machine_id,producer,cursor,code,lost,observed_from,observed_to)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, machine, producer, cursor, d.Code, strconv.FormatUint(d.Lost, 10), from, to)
	return err
}
