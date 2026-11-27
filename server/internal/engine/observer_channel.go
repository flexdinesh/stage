package engine

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/observe"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// ServeWorkers installs the observation receiver before the hub accepts workers.
func (e *Engine) ServeWorkers(hub *workerchan.Hub) {
	channel := observationChannel{hub: hub, observe: e.Observe}
	e.Observe.SetWatchTransport(channel)
	hub.Handle(observe.KindBatch, channel.receiveBatch)
}

type observationChannel struct {
	hub     *workerchan.Hub
	observe *observe.Service
}

func observationKinds(ready *workerchan.Ready) bool {
	return ready != nil && slices.Contains(ready.ChannelKinds, observe.KindWatch) && slices.Contains(ready.UpKinds, observe.KindBatch)
}

func (c observationChannel) Attachments() map[int64]observe.WatchAttachment {
	live := c.hub.ReadyMachines()
	out := make(map[int64]observe.WatchAttachment, len(live))
	for _, id := range live {
		ready, session := c.hub.Advertisement(id)
		if ready == nil {
			continue
		}
		a := observe.WatchAttachment{Session: session, Supported: observationKinds(ready)}
		for _, conn := range ready.Connections {
			a.Connections = append(a.Connections, conn.ConnectionID)
		}
		out[id] = a
	}
	return out
}

func (c observationChannel) receiveBatch(ctx context.Context, machine int64, env workerchan.Envelope) (any, error) {
	ready, attachment := c.hub.Advertisement(machine)
	if attachment != workerchan.CallingSession(ctx) || !observationKinds(ready) {
		return nil, &workerchan.RemoteError{Code: workerchan.CodeOperationUnavailable, Message: "Observation is unavailable on this attachment. Update Stage on both machines."}
	}
	batch, err := decodeBatch(env.Body)
	if err != nil {
		return nil, &workerchan.RemoteError{Code: "invalid_observation_batch", Message: "Malformed observation batch."}
	}
	ack, err := c.observe.CommitBatch(ctx, machine, batch)
	if ack.Resample {
		c.observe.RequestResample(machine)
	}
	var invalid *db.InvalidError
	switch {
	case err == nil:
		return ack, nil
	case errors.Is(err, observe.ErrWrongMachine):
		return nil, &workerchan.RemoteError{Code: "observation_wrong_machine", Message: err.Error()}
	case errors.Is(err, db.ErrObservationProducer):
		return nil, &workerchan.RemoteError{Code: "observation_producer", Message: err.Error()}
	case errors.Is(err, db.ErrObservationCursor):
		return nil, &workerchan.RemoteError{Code: "observation_cursor", Message: err.Error()}
	case errors.As(err, &invalid):
		return nil, &workerchan.RemoteError{Code: "invalid_observation_batch", Message: invalid.Error()}
	default:
		// A transient storage failure can leave a committed prefix. The
		// worker keeps its sealed batch and retries the suffix by replay.
		slog.Warn("worker observation commit incomplete", "machine", machine, "cursor", ack.Cursor)
		return ack, nil
	}
}

func decodeBatch(body json.RawMessage) (observe.Batch, error) {
	var wire struct {
		Producer string            `json:"producer"`
		Cursor   int64             `json:"cursor"`
		Records  []json.RawMessage `json:"records"`
	}
	if err := db.DecodeStrict(body, &wire); err != nil {
		return observe.Batch{}, err
	}
	if n := len(wire.Records); n == 0 || n > 100 || n > 1 && len(body) > 256<<10 {
		return observe.Batch{}, db.Invalid("observation batch exceeds the record or byte limit")
	}
	batch := observe.Batch{Producer: wire.Producer, Cursor: wire.Cursor, Records: make([]observe.Record, 0, len(wire.Records))}
	for _, raw := range wire.Records {
		var record observe.Record
		if err := db.DecodeStrict(raw, &record); err != nil {
			// Preserve the cursor and machine scope of an invalid record.
			// The sink will commit its rejection and continue through the batch.
			var header struct {
				Cursor       int64 `json:"cursor"`
				ConnectionID int64 `json:"connectionId"`
			}
			if err := json.Unmarshal(raw, &header); err != nil {
				return observe.Batch{}, err
			}
			record = observe.Record{Cursor: header.Cursor, ConnectionID: header.ConnectionID}
		}
		batch.Records = append(batch.Records, record)
	}
	return batch, nil
}

func (c observationChannel) ReplaceWatch(parent context.Context, machine int64, session string, w observe.Watch) error {
	ctx, cancel, err := c.hub.SessionContext(parent, machine, session)
	if err != nil {
		return err
	}
	defer cancel()
	raw, err := c.hub.Call(ctx, machine, observe.KindWatch, 0, w, 15*time.Second)
	if err != nil {
		return err
	}
	var ack observe.WatchAck
	if err := json.Unmarshal(raw, &ack); err != nil {
		return errors.New("worker returned an invalid observation producer")
	}
	if ctx.Err() != nil || c.hub.SessionID(machine) != session {
		return workerchan.ErrReplaced
	}
	return c.observe.BindWorkerProducer(ctx, machine, ack.Producer)
}
