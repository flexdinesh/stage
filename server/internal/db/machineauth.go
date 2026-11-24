package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// Machine credentials (docs/api.md "Machines"). A pairing code adds a
// remote machine once; the machine token it yields is that machine's
// worker credential on /api/worker/*. Both are selector.verifier values:
// prefix, row id, "_", random secret. Only the secret's SHA-256 is stored,
// and it is compared in constant time (secretMatches).
const (
	// MachineTokenPrefix starts every machine token.
	MachineTokenPrefix = "stage_mt_"
	pairingCodePrefix  = "stage_pc_"
	tokenSecretBytes   = 32
	pairingSecretBytes = 20
	// PairingTTL is how long a pairing code can be redeemed.
	PairingTTL = 10 * time.Minute
	// pairingKeep is how long used or expired codes stay before pruning.
	pairingKeep = 24 * time.Hour
	// MaxMachineLabel caps a machine label, in characters.
	MaxMachineLabel = 100
)

var (
	// ErrInvalidPairing: the code is unknown, wrong, used or expired. The
	// cases are indistinguishable on purpose.
	ErrInvalidPairing = errors.New("invalid or expired pairing code")
	// ErrInvalidMachineToken: the token is malformed, wrong, or its machine
	// was removed.
	ErrInvalidMachineToken = errors.New("invalid or revoked machine token")
)

var secretEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newSecret is n random bytes, base32 (lowercase, unpadded), and its hash.
func newSecret(n int) (string, []byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	s := strings.ToLower(secretEncoding.EncodeToString(b))
	h := sha256.Sum256([]byte(s))
	return s, h[:], nil
}

func formatSecret(prefix string, id int64, secret string) string {
	return prefix + strconv.FormatInt(id, 10) + "_" + secret
}

// parseSecret splits prefix<id>_<secret>.
func parseSecret(prefix, v string) (int64, string, bool) {
	rest, ok := strings.CutPrefix(v, prefix)
	if !ok {
		return 0, "", false
	}
	idText, secret, ok := strings.Cut(rest, "_")
	if !ok || secret == "" {
		return 0, "", false
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		return 0, "", false
	}
	return id, secret, true
}

// secretMatches compares secret's hash with the stored hash in constant
// time.
func secretMatches(stored []byte, secret string) bool {
	h := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(stored, h[:]) == 1
}

// IsMachineToken reports whether v has the machine token form (not whether
// it is valid).
func IsMachineToken(v string) bool { return strings.HasPrefix(v, MachineTokenPrefix) }

// machineLabel trims and checks a label ("" allowed when optional).
func machineLabel(label string, optional bool) (string, error) {
	label = strings.TrimSpace(label)
	switch {
	case label == "" && !optional:
		return "", Invalid("A machine needs a name.")
	case utf8.RuneCountInString(label) > MaxMachineLabel:
		return "", Invalid("A machine name can be at most %d characters.", MaxMachineLabel)
	}
	return label, nil
}

// Pairing is a new one-time pairing code. Code is shown once and never
// stored.
type Pairing struct {
	ID        int64
	Code      string
	Label     string
	ExpiresAt time.Time
}

// CreatePairing creates a pairing code valid for PairingTTL, created by the
// acting user. label (optional) names the machine it adds. Codes used or
// expired over a day ago are pruned.
func (s *Store) CreatePairing(ctx context.Context, label string) (Pairing, error) {
	label, err := machineLabel(label, true)
	if err != nil {
		return Pairing{}, err
	}
	secret, hash, err := newSecret(pairingSecretBytes)
	if err != nil {
		return Pairing{}, err
	}
	now := time.Now()
	p := Pairing{Label: label, ExpiresAt: now.Add(PairingTTL).UTC().Truncate(time.Microsecond)}
	err = s.e.Write(ctx, func(tx store.Tx) error {
		if err := tx.Machines().PrunePairings(ctx, now.Add(-pairingKeep)); err != nil {
			return err
		}
		p.ID, err = tx.Machines().InsertPairing(ctx, hash, label, ActingUser(ctx), p.ExpiresAt)
		return err
	})
	if err != nil {
		return Pairing{}, err
	}
	p.Code = formatSecret(pairingCodePrefix, p.ID, secret)
	return p, nil
}

// PairingState is where a pairing code stands: waiting (redeemable),
// joined (redeemed; MachineID is the machine it added) or expired.
type PairingState struct {
	ID        int64
	State     string
	ExpiresAt time.Time
	MachineID *int64
}

// PairingStatus reads a pairing code's state by its id (ErrNotFound when
// unknown, revoked or pruned). It never reveals the code.
func (s *Store) PairingStatus(ctx context.Context, id int64) (PairingState, error) {
	row, err := read(ctx, s, func(tx store.Tx) (store.PairingRow, error) { return tx.Machines().Pairing(ctx, id) })
	if err != nil {
		return PairingState{}, err
	}
	p := PairingState{ID: id, ExpiresAt: row.ExpiresAt, MachineID: row.MachineID}
	switch {
	case p.MachineID != nil:
		p.State = "joined"
	case row.Live:
		p.State = "waiting"
	default:
		p.State = "expired"
	}
	return p, nil
}

// RevokePairing deletes a pairing code that was not redeemed, so it can no
// longer add a machine (ErrNotFound when unknown; a redeemed code is a
// conflict: remove its machine instead).
func (s *Store) RevokePairing(ctx context.Context, id int64) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		row, err := tx.Machines().Pairing(ctx, id)
		if err != nil {
			return err
		}
		if row.MachineID != nil {
			return &ConflictError{Msg: "This pairing code already added a machine. Remove the machine instead.", Code: "pairing_redeemed"}
		}
		return tx.Machines().DeleteUnredeemedPairing(ctx, id)
	})
}

// MachineGrant is a redeemed pairing: the new machine and its token. Token
// is shown once and never stored.
type MachineGrant struct {
	MachineID int64
	Label     string
	Token     string
}

// RedeemPairing exchanges a pairing code for a new remote machine and its
// token, once. The machine is named by the pairing's label, else label
// (the worker's suggestion), else "Machine <id>"; its creator is the code's
// creator. version is the worker's build version. Any unusable code is
// ErrInvalidPairing.
func (s *Store) RedeemPairing(ctx context.Context, code, label, version string) (MachineGrant, error) {
	id, secret, ok := parseSecret(pairingCodePrefix, strings.TrimSpace(code))
	if !ok {
		return MachineGrant{}, ErrInvalidPairing
	}
	label, err := machineLabel(label, true)
	if err != nil {
		return MachineGrant{}, err
	}
	tokenSecret, tokenHash, err := newSecret(tokenSecretBytes)
	if err != nil {
		return MachineGrant{}, err
	}
	var g MachineGrant
	var tokenID int64
	err = s.e.Write(ctx, func(tx store.Tx) error {
		// Write may rerun fn (Postgres retry): start each run from scratch.
		g, tokenID = MachineGrant{}, 0
		m := tx.Machines()
		pair, err := m.RedeemablePairing(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return ErrInvalidPairing
		}
		if err != nil {
			return err
		}
		if !secretMatches(pair.Hash, secret) {
			return ErrInvalidPairing
		}
		g.Label = pair.Label
		if g.Label == "" {
			g.Label = label
		}
		if g.MachineID, err = m.InsertMachine(ctx, g.Label, pair.CreatedBy, strings.TrimSpace(version)); err != nil {
			return err
		}
		if g.Label == "" {
			g.Label = "Machine " + strconv.FormatInt(g.MachineID, 10)
			if err := m.SetLabel(ctx, g.MachineID, g.Label); err != nil {
				return err
			}
		}
		redeemed, err := m.MarkRedeemed(ctx, id, g.MachineID)
		if err != nil {
			return err
		}
		if !redeemed {
			return ErrInvalidPairing
		}
		tokenID, err = m.InsertToken(ctx, g.MachineID, tokenHash)
		return err
	})
	if err != nil {
		return MachineGrant{}, err
	}
	g.Token = formatSecret(MachineTokenPrefix, tokenID, tokenSecret)
	return g, nil
}

// AuthenticateMachine is the machine a token belongs to. A malformed or
// wrong token, or one whose machine was removed, is ErrInvalidMachineToken.
func (s *Store) AuthenticateMachine(ctx context.Context, token string) (int64, error) {
	id, secret, ok := parseSecret(MachineTokenPrefix, token)
	if !ok {
		return 0, ErrInvalidMachineToken
	}
	var machine int64
	var stored []byte
	err := s.e.Read(ctx, func(tx store.Tx) error {
		var err error
		machine, stored, err = tx.Machines().Token(ctx, id)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return 0, ErrInvalidMachineToken
	}
	if err != nil {
		return 0, err
	}
	if !secretMatches(stored, secret) {
		return 0, ErrInvalidMachineToken
	}
	return machine, nil
}

// TouchMachine records that a remote machine's worker was heard from at
// at (never moving last_seen_at back) and, when version is not empty, the
// build it runs. Removed machines and the local machine are left alone.
func (s *Store) TouchMachine(ctx context.Context, id int64, at time.Time, version string) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		return tx.Machines().Touch(ctx, id, at, strings.TrimSpace(version))
	})
}

type (
	MachineInfo       = store.MachineInfo
	MachineConnection = store.MachineConnection
)

// ListMachines lists machines not removed, local first.
func (s *Store) ListMachines(ctx context.Context) ([]MachineInfo, error) {
	return read(ctx, s, func(tx store.Tx) ([]MachineInfo, error) { return tx.Machines().List(ctx, 0) })
}

// GetMachineInfo reads a machine not removed (ErrNotFound otherwise).
func (s *Store) GetMachineInfo(ctx context.Context, id int64) (MachineInfo, error) {
	ms, err := read(ctx, s, func(tx store.Tx) ([]MachineInfo, error) { return tx.Machines().List(ctx, id) })
	if err != nil {
		return MachineInfo{}, err
	}
	if len(ms) == 0 {
		return MachineInfo{}, ErrNotFound
	}
	return ms[0], nil
}

// SetDefaultLocalLabel names the local machine only while its label is
// unset (empty, migration 0022); a name the user gave is kept. It reports
// whether it set the label.
func (s *Store) SetDefaultLocalLabel(ctx context.Context, label string) (bool, error) {
	label, err := machineLabel(label, false)
	if err != nil {
		return false, err
	}
	return write(ctx, s, func(tx store.Tx) (bool, error) { return tx.Machines().SetLabelIfEmpty(ctx, LocalMachine, label) })
}

// RenameMachine sets a machine's label.
func (s *Store) RenameMachine(ctx context.Context, id int64, label string) error {
	label, err := machineLabel(label, false)
	if err != nil {
		return err
	}
	renamed, err := write(ctx, s, func(tx store.Tx) (bool, error) { return tx.Machines().Rename(ctx, id, label) })
	if err == nil && !renamed {
		err = ErrNotFound
	}
	return err
}

// RemoveMachine removes a remote machine: its token is revoked and its
// active connections retire as history, under the rules of a replaced
// instance (a connection nothing references is deleted). Unlaunched tasks
// and schedules stay on the retired connections. The row stays, hidden, so
// history keeps its label. The local machine cannot be removed (409
// local_machine).
func (s *Store) RemoveMachine(ctx context.Context, id int64) error {
	if id == LocalMachine {
		return &ConflictError{Msg: "This computer runs the Stage server and cannot be removed.", Code: "local_machine"}
	}
	err := s.e.Write(ctx, func(tx store.Tx) error {
		removed, err := tx.Machines().MarkRemoved(ctx, id)
		if err != nil {
			return err
		}
		if !removed {
			return ErrNotFound
		}
		if err := tx.Machines().DeleteTokens(ctx, id); err != nil {
			return err
		}
		c := tx.Connections()
		ids, err := c.ActiveOnMachine(ctx, id)
		if err != nil {
			return err
		}
		for _, conn := range ids {
			referenced, err := connectionReferenced(ctx, tx, conn)
			if err != nil {
				return err
			}
			if !referenced {
				if err := c.Delete(ctx, conn); err != nil {
					return err
				}
				continue
			}
			if err := c.Retire(ctx, conn); err != nil {
				return err
			}
			if err := c.ClearDefault(ctx, conn); err != nil {
				return err
			}
		}
		return nil
	})
	s.dropConnections()
	return err
}

// connectionReferenced: relations (HasRelations) or unlaunched tasks and
// schedules point at the connection, so it must stay as history.
func connectionReferenced(ctx context.Context, tx store.Tx, id int64) (bool, error) {
	related, err := tx.Connections().HasRelations(ctx, id)
	if err != nil || related {
		return related, err
	}
	return tx.Connections().HasWork(ctx, id)
}
