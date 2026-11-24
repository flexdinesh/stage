package db

import (
	"context"
	"errors"

	"github.com/flexdinesh/stage/server/internal/db/store"
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

// SQL (transitional, for db code not yet behind repositories) for the legacy T3 connection id (the local machine's active T3: the
// owner of session-less thread ids written before connections existed), and
// for every T3 connection id on any machine.
const (
	legacyT3SQL      = `(SELECT id FROM harness_connections WHERE machine_id=1 AND harness='t3' AND active=1)`
	t3ConnectionsSQL = `(SELECT id FROM harness_connections WHERE harness='t3')`
)

type connectionSnapshot struct{ rows []store.ConnectionRow }

func (s *Store) snapshot() connectionSnapshot {
	if c := s.conns.Load(); c != nil {
		return *c
	}
	list, err := read(context.Background(), s, func(tx store.Tx) ([]store.ConnectionRow, error) { return tx.Connections().Rows(context.Background()) })
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
		if c.Machine == machine && c.Harness == harness && c.Active {
			return c.ID
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
		if c.Active {
			out = append(out, ActiveConnection{ID: c.ID, Machine: c.Machine, Harness: c.Harness})
		}
	}
	return out
}

// ConnectionMachine is the machine of a connection (0 when unknown).
func (s *Store) ConnectionMachine(id int64) int64 {
	for _, c := range s.snapshot().rows {
		if c.ID == id {
			return c.Machine
		}
	}
	return 0
}

// Harness of a connection ("" when unknown).
func (s *Store) Harness(id int64) string {
	for _, c := range s.snapshot().rows {
		if c.ID == id {
			return c.Harness
		}
	}
	return ""
}

// IsT3 reports whether a connection (active or retired) is a T3 instance.
func (s *Store) IsT3(id int64) bool { return s.Harness(id) == HarnessT3 }

// ConnectionActive reports whether a connection is its harness's active one.
func (s *Store) ConnectionActive(id int64) bool {
	for _, c := range s.snapshot().rows {
		if c.ID == id {
			return c.Active
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
	err := s.e.Write(ctx, func(tx store.Tx) error {
		c := tx.Connections()
		cur, err := c.Active(ctx, machine, kind)
		if errors.Is(err, ErrNotFound) {
			out.New, err = c.Insert(ctx, machine, kind, identity, label, d.StableIdentity)
			return err
		}
		if err != nil {
			return err
		}
		out.Old, out.New = cur.ID, cur.ID
		target := revive
		if target != 0 {
			ok, err := c.Restorable(ctx, target, machine, kind)
			if err != nil {
				return err
			}
			if !ok {
				return Conflict("That %s connection can no longer be restored.", d.Name)
			}
		} else {
			if cur.Identity == identity {
				return c.SetLabel(ctx, cur.ID, label)
			}
			if d.StableIdentity {
				id, err := c.RetiredWithIdentity(ctx, machine, kind, identity)
				if err != nil && !errors.Is(err, ErrNotFound) {
					return err
				}
				target = id
			}
		}
		related, err := c.HasRelations(ctx, cur.ID)
		if err != nil {
			return err
		}
		if target == 0 && (cur.Identity == "" || !related) {
			if cur.Identity != "" {
				if err := c.ClearDiscovery(ctx, cur.ID); err != nil {
					return err
				}
			}
			return c.SetIdentity(ctx, cur.ID, identity, label)
		}
		if err := c.Retire(ctx, cur.ID); err != nil {
			return err
		}
		if target != 0 {
			if err := c.Revive(ctx, target, label, identity); err != nil {
				return err
			}
			out.New, out.Revived = target, true
		} else if out.New, err = c.InsertReplacement(ctx, machine, kind, identity, label, cur.AutoComplete, d.StableIdentity, cur.ID); err != nil {
			return err
		}
		out.Changed = true
		if err := c.FollowReplacement(ctx, cur.ID, out.New); err != nil {
			return err
		}
		if !related {
			return c.Delete(ctx, cur.ID)
		}
		return nil
	})
	s.dropConnections()
	return out, err
}

// Move is a connection whose instance now runs on another machine: Old is
// active there but holds the same stable identity as New, active elsewhere.
type Move = store.Move

// MovedConnections lists active connections whose stable identity is also the
// identity of an active connection of the same harness on another machine.
// Whether a pair really moved (Old disconnected, New connected) is a runtime
// question the caller answers before RetireConnection.
func (s *Store) MovedConnections(ctx context.Context) ([]Move, error) {
	return read(ctx, s, func(tx store.Tx) ([]Move, error) { return tx.Connections().Moved(ctx) })
}

// RetireConnection retires an active connection. With a replacement (an
// active connection of the same harness), the default, unlaunched backlog
// tasks and schedules follow it, and a retired connection that holds nothing
// is deleted. Without one, the default is cleared and the rest stays; the
// tasks keep their history, read-only. 404 unknown, 409 already retired or an
// unusable replacement.
func (s *Store) RetireConnection(ctx context.Context, id, replacement int64) error {
	err := s.e.Write(ctx, func(tx store.Tx) error { return retireTx(ctx, tx, id, replacement) })
	s.dropConnections()
	return err
}

func retireTx(ctx context.Context, tx store.Tx, id, replacement int64) error {
	c := tx.Connections()
	harness, active, err := c.RetireState(ctx, id)
	if err != nil {
		return err
	}
	if !active {
		return Conflict("This connection is already retired.")
	}
	if replacement != 0 {
		ok, err := c.IsActiveReplacement(ctx, replacement, harness, id)
		if err != nil {
			return err
		}
		if !ok {
			return Conflict("The replacement must be another active %s connection.", harness)
		}
	}
	related, err := c.HasRelations(ctx, id)
	if err != nil {
		return err
	}
	if err := c.Retire(ctx, id); err != nil {
		return err
	}
	if replacement == 0 {
		return c.ClearDefault(ctx, id)
	}
	if err := c.FollowReplacement(ctx, id, replacement); err != nil {
		return err
	}
	if !related {
		return c.Delete(ctx, id)
	}
	return nil
}

// SetConnectionIdentity records a new address of the same instance (an
// OpenCode service that proved continuity).
func (s *Store) SetConnectionIdentity(ctx context.Context, id int64, identity, label string) error {
	if label == "" {
		label = identity
	}
	err := s.e.Write(ctx, func(tx store.Tx) error { return tx.Connections().SetIdentity(ctx, id, identity, label) })
	s.dropConnections()
	return err
}

// RetiredConnections lists the retired, not forgotten connections of a
// harness on machine, newest first.
func (s *Store) RetiredConnections(ctx context.Context, machine int64, harness string) ([]int64, error) {
	return read(ctx, s, func(tx store.Tx) ([]int64, error) { return tx.Connections().Retired(ctx, machine, harness) })
}

// ForgetConnection lets go of a retired connection. Its history stays: tasks
// keep it and its label. It can no longer be restored, its project bindings
// no longer ask for repair, and its discovery and shared paths are dropped.
// A connection nothing references is deleted outright.
func (s *Store) ForgetConnection(ctx context.Context, id int64) error {
	err := s.e.Write(ctx, func(tx store.Tx) error { return forgetTx(ctx, tx, id) })
	s.dropConnections()
	return err
}

// RetireAndForget lets go of an active connection the caller found
// disconnected: it retires (RetireConnection without a replacement) and is
// forgotten (ForgetConnection) at once.
func (s *Store) RetireAndForget(ctx context.Context, id int64) error {
	err := s.e.Write(ctx, func(tx store.Tx) error {
		if err := retireTx(ctx, tx, id, 0); err != nil {
			return err
		}
		return forgetTx(ctx, tx, id)
	})
	s.dropConnections()
	return err
}

func forgetTx(ctx context.Context, tx store.Tx, id int64) error {
	c := tx.Connections()
	active, forgotten, err := c.ForgetState(ctx, id)
	if errors.Is(err, ErrNotFound) || (err == nil && forgotten) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if active {
		return Conflict("This connection is in use. Only a replaced connection can be forgotten.")
	}
	referenced, err := c.Referenced(ctx, id)
	if err != nil {
		return err
	}
	if !referenced {
		if err := c.DeleteBindings(ctx, id); err != nil {
			return err
		}
		return c.Delete(ctx, id)
	}
	return c.Forget(ctx, id)
}

// legacyT3ConnectionID owns rows written before connections existed.
const legacyT3ConnectionID int64 = 1

// legacyT3Tx is the legacy T3 connection (LegacyT3ID) inside a unit of work.
func legacyT3Tx(ctx context.Context, tx store.Tx) (int64, error) {
	id, err := tx.Connections().LegacyT3(ctx)
	if err == nil && id == 0 {
		err = Invalid("no T3 connection; pair T3 in Settings")
	}
	return id, err
}

// connectionUsable refuses new work on an unknown or replaced connection.
func connectionUsable(ctx context.Context, tx store.Tx, id int64) error {
	if err := requireConnection(ctx, tx, id); err != nil {
		return err
	}
	active, err := tx.Connections().IsActive(ctx, id)
	if err != nil {
		return err
	}
	if !active {
		return ConnectionRetired()
	}
	return nil
}

// legacyT3 (transitional raw SQL) is legacyT3Tx for a raw transaction.
func legacyT3(ctx context.Context, tx *Tx) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(`+legacyT3SQL+`,0)`).Scan(&id)
	if err == nil && id == 0 {
		err = Invalid("no T3 connection; pair T3 in Settings")
	}
	return id, err
}

// harnessIs (transitional raw SQL) reports whether a connection belongs to harness.
func harnessIs(ctx context.Context, tx *Tx, id int64, harness string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM harness_connections WHERE id=$1 AND harness=$2)`, id, harness).Scan(&ok)
	return ok, err
}
