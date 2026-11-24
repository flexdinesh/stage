package store

import (
	"context"
	"time"
)

// Machine runs harnesses: their connections, workspaces and processes.
// One local machine exists until workers do (migration 0015).
type Machine struct {
	ID        int64  `json:"id"`
	Label     string `json:"label"`
	CreatedBy int64  `json:"createdBy"` // user who added it (the local machine: the default user)
}

// MachineInfo is a machine as listed: its row, what its worker last
// reported, and its active connections.
type MachineInfo struct {
	Machine
	CreatedAt   time.Time
	LastSeenAt  *time.Time
	Version     string
	Connections []MachineConnection
}

// MachineConnection is an active harness connection of a machine.
type MachineConnection struct {
	ID      int64  `json:"id"`
	Harness string `json:"harness"`
	Label   string `json:"label"`
}

// PairingRow is a stored pairing code: Live while unexpired.
type PairingRow struct {
	ExpiresAt time.Time
	MachineID *int64
	Live      bool
}

// PairingSecret is a redeemable pairing code's stored hash and owner.
type PairingSecret struct {
	Hash      []byte
	Label     string
	CreatedBy int64
}

// Machines is the machines repository: machines, pairing codes and
// machine tokens (secrets stored as hashes only).
type Machines interface {
	Get(ctx context.Context, id int64) (Machine, error)
	// List lists machines not removed (only id when nonzero), by id, with
	// their active connections.
	List(ctx context.Context, id int64) ([]MachineInfo, error)
	// PrunePairings deletes codes redeemed or expired before t.
	PrunePairings(ctx context.Context, before time.Time) error
	InsertPairing(ctx context.Context, hash []byte, label string, createdBy int64, expiresAt time.Time) (int64, error)
	// Pairing reads a code (ErrNotFound when unknown).
	Pairing(ctx context.Context, id int64) (PairingRow, error)
	// DeleteUnredeemedPairing deletes a code not redeemed.
	DeleteUnredeemedPairing(ctx context.Context, id int64) error
	// RedeemablePairing reads an unredeemed, unexpired code (ErrNotFound
	// otherwise).
	RedeemablePairing(ctx context.Context, id int64) (PairingSecret, error)
	// MarkRedeemed redeems an unredeemed code for machine; false when it
	// was already redeemed.
	MarkRedeemed(ctx context.Context, id, machine int64) (bool, error)
	// InsertMachine adds a remote machine heard from now.
	InsertMachine(ctx context.Context, label string, createdBy int64, version string) (int64, error)
	SetLabel(ctx context.Context, id int64, label string) error
	// SetLabelIfEmpty sets an empty label; false when it was set.
	SetLabelIfEmpty(ctx context.Context, id int64, label string) (bool, error)
	// Rename sets a label of a machine not removed; false when none.
	Rename(ctx context.Context, id int64, label string) (bool, error)
	InsertToken(ctx context.Context, machine int64, hash []byte) (int64, error)
	// Token reads a token of a machine not removed (ErrNotFound otherwise).
	Token(ctx context.Context, id int64) (machine int64, hash []byte, err error)
	// MarkRemoved hides a machine not removed; false when none.
	MarkRemoved(ctx context.Context, id int64) (bool, error)
	DeleteTokens(ctx context.Context, machine int64) error
	// Touch records a remote machine heard from at (never moving back) and
	// version when not empty; removed and local machines are left alone.
	Touch(ctx context.Context, id int64, at time.Time, version string) error
}
