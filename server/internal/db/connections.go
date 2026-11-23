package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/flexdinesh/stage/server/internal/harness"
)

// LocalMachine is the machine this server runs on (migration 0015). Every
// connection belongs to it until workers exist; legacy T3 thread ids belong
// to its active T3 connection.
const LocalMachine int64 = 1

// Harness names.
const (
	HarnessT3       = "t3"
	HarnessOpenCode = "opencode"
)

// SQL for the legacy T3 connection id (the local machine's active T3: the
// owner of session-less thread ids written before connections existed), and
// for every T3 connection id on any machine.
const (
	legacyT3SQL      = `(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1)`
	t3ConnectionsSQL = `(SELECT id FROM harness_connections WHERE harness='t3')`
)

type connectionRow struct {
	id      int64
	machine int64
	harness string
	active  bool
}

type connectionSnapshot struct{ rows []connectionRow }

func (s *Store) snapshot() connectionSnapshot {
	if c := s.conns.Load(); c != nil {
		return *c
	}
	rows, err := s.pool.Query(context.Background(), `SELECT id,machine_id,harness,active FROM harness_connections ORDER BY id`)
	if err != nil {
		return connectionSnapshot{}
	}
	list, err := collectRows(rows, func(r rowScanner) (connectionRow, error) {
		var c connectionRow
		err := r.Scan(&c.id, &c.machine, &c.harness, &c.active)
		return c, err
	})
	if err != nil {
		return connectionSnapshot{}
	}
	snap := connectionSnapshot{rows: list}
	s.conns.Store(&snap)
	return snap
}

// dropConnections forgets the cached connections after a write.
func (s *Store) dropConnections() { s.conns.Store(nil) }

// ActiveID is the active connection of a harness on a machine (0 when none).
func (s *Store) ActiveID(machine int64, harness string) int64 {
	for _, c := range s.snapshot().rows {
		if c.machine == machine && c.harness == harness && c.active {
			return c.id
		}
	}
	return 0
}

// LegacyT3ID is the local machine's active T3 connection: the default for
// legacy thread ids and requests that name no connection.
func (s *Store) LegacyT3ID() int64 { return s.ActiveID(LocalMachine, HarnessT3) }

// ActiveConnection is one active harness connection.
type ActiveConnection struct {
	ID, Machine int64
	Harness     string
}

// ActiveConnections lists the active connection of every harness on every
// machine, by id.
func (s *Store) ActiveConnections() []ActiveConnection {
	var out []ActiveConnection
	for _, c := range s.snapshot().rows {
		if c.active {
			out = append(out, ActiveConnection{ID: c.id, Machine: c.machine, Harness: c.harness})
		}
	}
	return out
}

// ConnectionMachine is the machine of a connection (0 when unknown).
func (s *Store) ConnectionMachine(id int64) int64 {
	for _, c := range s.snapshot().rows {
		if c.id == id {
			return c.machine
		}
	}
	return 0
}

// Harness of a connection ("" when unknown).
func (s *Store) Harness(id int64) string {
	for _, c := range s.snapshot().rows {
		if c.id == id {
			return c.harness
		}
	}
	return ""
}

// IsT3 reports whether a connection (active or retired) is a T3 instance.
func (s *Store) IsT3(id int64) bool { return s.Harness(id) == HarnessT3 }

// ConnectionActive reports whether a connection is its harness's active one.
func (s *Store) ConnectionActive(id int64) bool {
	for _, c := range s.snapshot().rows {
		if c.id == id {
			return c.active
		}
	}
	return false
}

// Switch is the outcome of SwitchInstance.
type Switch struct {
	// Old and New are the previous and current active connection.
	Old, New int64
	// Changed: New replaced Old (Old retired, or deleted when it held nothing).
	Changed bool
	// Revived: New is a retired connection made active again.
	Revived bool
}

type activeConnection struct {
	id           int64
	identity     string
	autoComplete bool
}

// SwitchInstance makes the instance with identity the active connection of
// harness on machine. The same identity keeps the connection. A connection with no
// identity yet, or holding no relations, is restamped in place. Otherwise
// the active connection retires and keeps its relations, and the instance
// gets a retired connection with that identity back when the harness's
// identity is stable (or revive, a retired connection the caller matched),
// else a new connection. Unlaunched backlog
// tasks and schedules follow the active connection; they launch only after
// the user binds the project there. A connection of another machine is never
// revived.
func (s *Store) SwitchInstance(ctx context.Context, machine int64, d harness.Descriptor, identity, label string, revive int64) (Switch, error) {
	kind := d.Kind
	if identity == "" && revive == 0 {
		return Switch{}, Invalid("an instance identity is required")
	}
	if label == "" {
		label = identity
	}
	var out Switch
	err := s.inTx(ctx, func(tx *Tx) error {
		var cur activeConnection
		err := tx.QueryRow(ctx, `SELECT id,identity,auto_complete FROM harness_connections WHERE machine_id=$2 AND harness=$1 AND active=1`, kind, machine).Scan(&cur.id, &cur.identity, &cur.autoComplete)
		if errors.Is(err, sql.ErrNoRows) {
			return tx.QueryRow(ctx, `INSERT INTO harness_connections(machine_id,harness,identity,label,stable_identity) VALUES($4,$1,$2,$3,$5) RETURNING id`, kind, identity, label, machine, d.StableIdentity).Scan(&out.New)
		}
		if err != nil {
			return err
		}
		out.Old, out.New = cur.id, cur.id
		target := revive
		if target != 0 {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM harness_connections WHERE id=$1 AND machine_id=$3 AND harness=$2 AND active=0 AND forgotten_at IS NULL)`, target, kind, machine).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return Conflict("That %s connection can no longer be restored.", d.Name)
			}
		} else {
			if cur.identity == identity {
				_, err := tx.Exec(ctx, `UPDATE harness_connections SET label=$2 WHERE id=$1`, cur.id, label)
				return err
			}
			if d.StableIdentity {
				err := tx.QueryRow(ctx, `SELECT id FROM harness_connections WHERE machine_id=$3 AND harness=$2 AND active=0 AND identity=$1`, identity, kind, machine).Scan(&target)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
		}
		related, err := hasRelations(ctx, tx, cur.id)
		if err != nil {
			return err
		}
		if target == 0 && (cur.identity == "" || !related) {
			if cur.identity != "" {
				if _, err := tx.Exec(ctx, `DELETE FROM external_projects WHERE connection_id=$1`, cur.id); err != nil {
					return err
				}
			}
			_, err := tx.Exec(ctx, `UPDATE harness_connections SET identity=$2,label=$3 WHERE id=$1`, cur.id, identity, label)
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE harness_connections SET active=0,retired_at=stage_now() WHERE id=$1`, cur.id); err != nil {
			return err
		}
		if target != 0 {
			if _, err := tx.Exec(ctx, `UPDATE harness_connections SET active=1,retired_at=NULL,label=CASE WHEN $2<>'' THEN $2 ELSE label END,identity=CASE WHEN $3<>'' THEN $3 ELSE identity END WHERE id=$1`, target, label, identity); err != nil {
				return err
			}
			out.New, out.Revived = target, true
		} else if err := tx.QueryRow(ctx, `INSERT INTO harness_connections(machine_id,harness,identity,label,auto_complete,stable_identity,new_task_config) VALUES($5,$1,$2,$3,$4,$6,(SELECT new_task_config FROM harness_connections WHERE id=$7)) RETURNING id`, kind, identity, label, cur.autoComplete, machine, d.StableIdentity, cur.id).Scan(&out.New); err != nil {
			return err
		}
		out.Changed = true
		if err := followReplacement(ctx, tx, cur.id, out.New); err != nil {
			return err
		}
		if !related {
			return deleteConnection(ctx, tx, cur.id)
		}
		return nil
	})
	s.dropConnections()
	return out, err
}

// followReplacement moves what follows the active connection from a retired
// one to its replacement: the default connection, unlaunched backlog tasks and
// schedules. They launch only after the user binds the project there.
func followReplacement(ctx context.Context, tx *Tx, from, to int64) error {
	for _, q := range []string{
		`UPDATE preferences SET default_connection_id=$2 WHERE default_connection_id=$1`,
		`UPDATE tasks SET connection_id=$2,updated_at=stage_now() WHERE connection_id=$1 AND state='backlog' AND session_id IS NULL`,
		`UPDATE schedules SET connection_id=$2,updated_at=stage_now() WHERE connection_id=$1`,
	} {
		if _, err := tx.Exec(ctx, q, from, to); err != nil {
			return err
		}
	}
	return nil
}

// Move is a connection whose instance now runs on another machine: Old is
// active there but holds the same stable identity as New, active elsewhere.
type Move struct {
	Old, New int64
}

// MovedConnections lists active connections whose stable identity is also the
// identity of an active connection of the same harness on another machine.
// Whether a pair really moved (Old disconnected, New connected) is a runtime
// question the caller answers before RetireConnection.
func (s *Store) MovedConnections(ctx context.Context) ([]Move, error) {
	rows, err := s.pool.Query(ctx, `SELECT o.id,n.id FROM harness_connections o JOIN harness_connections n
 ON n.harness=o.harness AND n.identity=o.identity AND n.machine_id<>o.machine_id AND n.active=1 AND n.stable_identity=1
 WHERE o.active=1 AND o.identity<>'' AND o.stable_identity=1 ORDER BY o.id,n.id`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (Move, error) {
		var m Move
		err := r.Scan(&m.Old, &m.New)
		return m, err
	})
}

// RetireConnection retires an active connection. With a replacement (an
// active connection of the same harness), the default, unlaunched backlog
// tasks and schedules follow it, and a retired connection that holds nothing
// is deleted. Without one, the default is cleared and the rest stays; the
// tasks keep their history, read-only. 404 unknown, 409 already retired or an
// unusable replacement.
func (s *Store) RetireConnection(ctx context.Context, id, replacement int64) error {
	err := s.inTx(ctx, func(tx *Tx) error { return retireTx(ctx, tx, id, replacement) })
	s.dropConnections()
	return err
}

func retireTx(ctx context.Context, tx *Tx, id, replacement int64) error {
	var harness string
	var active bool
	err := tx.QueryRow(ctx, `SELECT harness,active FROM harness_connections WHERE id=$1 AND forgotten_at IS NULL`, id).Scan(&harness, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !active {
		return Conflict("This connection is already retired.")
	}
	if replacement != 0 {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM harness_connections WHERE id=$1 AND harness=$2 AND active=1 AND id<>$3)`, replacement, harness, id).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return Conflict("The replacement must be another active %s connection.", harness)
		}
	}
	related, err := hasRelations(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE harness_connections SET active=0,retired_at=stage_now() WHERE id=$1`, id); err != nil {
		return err
	}
	if replacement == 0 {
		_, err := tx.Exec(ctx, `UPDATE preferences SET default_connection_id=NULL WHERE default_connection_id=$1`, id)
		return err
	}
	if err := followReplacement(ctx, tx, id, replacement); err != nil {
		return err
	}
	if !related {
		return deleteConnection(ctx, tx, id)
	}
	return nil
}

// hasRelations: bindings, sessions or launched tasks tie a connection to its
// instance. Unlaunched backlog tasks and schedules do not; they follow.
func hasRelations(ctx context.Context, tx *Tx, id int64) (bool, error) {
	var related bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_bindings WHERE connection_id=$1)
 OR EXISTS(SELECT 1 FROM session_identities WHERE connection_id=$1)
 OR EXISTS(SELECT 1 FROM task_launch_destinations WHERE connection_id=$1)
 OR EXISTS(SELECT 1 FROM tasks WHERE connection_id=$1 AND NOT (state='backlog' AND session_id IS NULL))`, id).Scan(&related)
	return related, err
}

func deleteConnection(ctx context.Context, tx *Tx, id int64) error {
	for _, q := range []string{
		`DELETE FROM external_projects WHERE connection_id=$1`,
		`DELETE FROM shared_paths WHERE connection_id=$1`,
		`DELETE FROM harness_connections WHERE id=$1`,
	} {
		if _, err := tx.Exec(ctx, q, id); err != nil {
			return err
		}
	}
	return nil
}

// SetConnectionIdentity records a new address of the same instance (an
// OpenCode service that proved continuity).
func (s *Store) SetConnectionIdentity(ctx context.Context, id int64, identity, label string) error {
	if label == "" {
		label = identity
	}
	_, err := s.pool.Exec(ctx, `UPDATE harness_connections SET identity=$2,label=$3 WHERE id=$1`, id, identity, label)
	s.dropConnections()
	return err
}

// RetiredConnections lists the retired, not forgotten connections of a
// harness on machine, newest first.
func (s *Store) RetiredConnections(ctx context.Context, machine int64, harness string) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM harness_connections WHERE machine_id=$2 AND harness=$1 AND active=0 AND forgotten_at IS NULL ORDER BY retired_at DESC,id DESC`, harness, machine)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (int64, error) {
		var id int64
		err := r.Scan(&id)
		return id, err
	})
}

// ForgetConnection lets go of a retired connection. Its history stays: tasks
// keep it and its label. It can no longer be restored, its project bindings
// no longer ask for repair, and its discovery and shared paths are dropped.
// A connection nothing references is deleted outright.
func (s *Store) ForgetConnection(ctx context.Context, id int64) error {
	err := s.inTx(ctx, func(tx *Tx) error { return forgetTx(ctx, tx, id) })
	s.dropConnections()
	return err
}

// RetireAndForget lets go of an active connection the caller found
// disconnected: it retires (RetireConnection without a replacement) and is
// forgotten (ForgetConnection) at once.
func (s *Store) RetireAndForget(ctx context.Context, id int64) error {
	err := s.inTx(ctx, func(tx *Tx) error {
		if err := retireTx(ctx, tx, id, 0); err != nil {
			return err
		}
		return forgetTx(ctx, tx, id)
	})
	s.dropConnections()
	return err
}

func forgetTx(ctx context.Context, tx *Tx, id int64) error {
	var active bool
	var forgotten *string
	err := tx.QueryRow(ctx, `SELECT active,forgotten_at FROM harness_connections WHERE id=$1`, id).Scan(&active, &forgotten)
	if errors.Is(err, sql.ErrNoRows) || forgotten != nil {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if active {
		return Conflict("This connection is in use. Only a replaced connection can be forgotten.")
	}
	var referenced bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE connection_id=$1)
 OR EXISTS(SELECT 1 FROM schedules WHERE connection_id=$1)
 OR EXISTS(SELECT 1 FROM session_identities WHERE connection_id=$1)
 OR EXISTS(SELECT 1 FROM task_launch_destinations WHERE connection_id=$1)`, id).Scan(&referenced); err != nil {
		return err
	}
	if !referenced {
		if _, err := tx.Exec(ctx, `DELETE FROM project_bindings WHERE connection_id=$1`, id); err != nil {
			return err
		}
		return deleteConnection(ctx, tx, id)
	}
	for _, q := range []string{
		`UPDATE harness_connections SET identity='',forgotten_at=stage_now() WHERE id=$1`,
		`UPDATE project_bindings SET active=0 WHERE connection_id=$1`,
		`DELETE FROM external_projects WHERE connection_id=$1`,
		`DELETE FROM shared_paths WHERE connection_id=$1`,
	} {
		if _, err := tx.Exec(ctx, q, id); err != nil {
			return err
		}
	}
	return nil
}

// legacyT3ConnectionID owns rows written before connections existed.
const legacyT3ConnectionID int64 = 1

// legacyT3 is the legacy T3 connection (LegacyT3ID) inside a transaction.
func legacyT3(ctx context.Context, tx *Tx) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(`+legacyT3SQL+`,0)`).Scan(&id)
	if err == nil && id == 0 {
		err = Invalid("no T3 connection; pair T3 in Settings")
	}
	return id, err
}

// harnessIs reports whether a connection belongs to harness.
func harnessIs(ctx context.Context, tx *Tx, id int64, harness string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM harness_connections WHERE id=$1 AND harness=$2)`, id, harness).Scan(&ok)
	return ok, err
}
