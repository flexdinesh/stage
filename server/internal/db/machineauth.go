package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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
	err = s.inTx(ctx, func(tx *Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM pairing_codes WHERE COALESCE(redeemed_at,expires_at)<$1`, formatTime(now.Add(-pairingKeep))); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO pairing_codes(secret_hash,label,created_by_user_id,expires_at) VALUES($1,$2,$3,$4) RETURNING id`,
			hash, label, ActingUser(ctx), formatTime(p.ExpiresAt)).Scan(&p.ID)
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
	p := PairingState{ID: id}
	var live bool
	err := s.pool.QueryRow(ctx, `SELECT expires_at,machine_id,expires_at>stage_now() FROM pairing_codes WHERE id=$1`, id).Scan(&p.ExpiresAt, &p.MachineID, &live)
	if errors.Is(err, sql.ErrNoRows) {
		return PairingState{}, ErrNotFound
	}
	if err != nil {
		return PairingState{}, err
	}
	switch {
	case p.MachineID != nil:
		p.State = "joined"
	case live:
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
	return s.inTx(ctx, func(tx *Tx) error {
		var machine *int64
		err := tx.QueryRow(ctx, `SELECT machine_id FROM pairing_codes WHERE id=$1`, id).Scan(&machine)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if machine != nil {
			return &ConflictError{Msg: "This pairing code already added a machine. Remove the machine instead.", Code: "pairing_redeemed"}
		}
		_, err = tx.Exec(ctx, `DELETE FROM pairing_codes WHERE id=$1 AND redeemed_at IS NULL`, id)
		return err
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
	err = s.inTx(ctx, func(tx *Tx) error {
		var stored []byte
		var pairLabel string
		var creator int64
		err := tx.QueryRow(ctx, `SELECT secret_hash,label,created_by_user_id FROM pairing_codes WHERE id=$1 AND redeemed_at IS NULL AND expires_at>stage_now()`, id).Scan(&stored, &pairLabel, &creator)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidPairing
		}
		if err != nil {
			return err
		}
		if !secretMatches(stored, secret) {
			return ErrInvalidPairing
		}
		g.Label = pairLabel
		if g.Label == "" {
			g.Label = label
		}
		if err := tx.QueryRow(ctx, `INSERT INTO machines(label,created_by_user_id,last_seen_at,version) VALUES($1,$2,stage_now(),$3) RETURNING id`, g.Label, creator, strings.TrimSpace(version)).Scan(&g.MachineID); err != nil {
			return err
		}
		if g.Label == "" {
			g.Label = "Machine " + strconv.FormatInt(g.MachineID, 10)
			if _, err := tx.Exec(ctx, `UPDATE machines SET label=$2 WHERE id=$1`, g.MachineID, g.Label); err != nil {
				return err
			}
		}
		res, err := tx.Exec(ctx, `UPDATE pairing_codes SET redeemed_at=stage_now(),machine_id=$2 WHERE id=$1 AND redeemed_at IS NULL`, id, g.MachineID)
		if err != nil {
			return err
		}
		if res.RowsAffected() != 1 {
			return ErrInvalidPairing
		}
		return tx.QueryRow(ctx, `INSERT INTO machine_tokens(machine_id,secret_hash) VALUES($1,$2) RETURNING id`, g.MachineID, tokenHash).Scan(&tokenID)
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
	err := s.pool.QueryRow(ctx, `SELECT t.machine_id,t.secret_hash FROM machine_tokens t JOIN machines m ON m.id=t.machine_id WHERE t.id=$1 AND m.removed_at IS NULL`, id).Scan(&machine, &stored)
	if errors.Is(err, sql.ErrNoRows) {
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
	_, err := s.pool.Exec(ctx, `UPDATE machines SET last_seen_at=CASE WHEN last_seen_at IS NULL OR last_seen_at<$2 THEN $2 ELSE last_seen_at END, version=CASE WHEN $3<>'' THEN $3 ELSE version END WHERE id=$1 AND id<>1 AND removed_at IS NULL`, id, at, strings.TrimSpace(version))
	return err
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

// ListMachines lists machines not removed, local first.
func (s *Store) ListMachines(ctx context.Context) ([]MachineInfo, error) {
	return s.machineInfos(ctx, 0)
}

// GetMachineInfo reads a machine not removed (ErrNotFound otherwise).
func (s *Store) GetMachineInfo(ctx context.Context, id int64) (MachineInfo, error) {
	ms, err := s.machineInfos(ctx, id)
	if err != nil {
		return MachineInfo{}, err
	}
	if len(ms) == 0 {
		return MachineInfo{}, ErrNotFound
	}
	return ms[0], nil
}

// machineInfos lists machines not removed (only id when nonzero).
func (s *Store) machineInfos(ctx context.Context, id int64) ([]MachineInfo, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,label,created_by_user_id,created_at,last_seen_at,version FROM machines WHERE removed_at IS NULL AND ($1=0 OR id=$1) ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	ms, err := collectRows(rows, func(r rowScanner) (MachineInfo, error) {
		var m MachineInfo
		err := r.Scan(&m.ID, &m.Label, &m.CreatedBy, &m.CreatedAt, &m.LastSeenAt, &m.Version)
		m.Connections = []MachineConnection{}
		return m, err
	})
	if err != nil || len(ms) == 0 {
		return ms, err
	}
	rows, err = s.pool.Query(ctx, `SELECT machine_id,id,harness,label FROM harness_connections WHERE active=1 AND ($1=0 OR machine_id=$1) ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	type conn struct {
		machine int64
		c       MachineConnection
	}
	cs, err := collectRows(rows, func(r rowScanner) (conn, error) {
		var c conn
		err := r.Scan(&c.machine, &c.c.ID, &c.c.Harness, &c.c.Label)
		return c, err
	})
	if err != nil {
		return nil, err
	}
	for i := range ms {
		for _, c := range cs {
			if c.machine == ms[i].ID {
				ms[i].Connections = append(ms[i].Connections, c.c)
			}
		}
	}
	return ms, nil
}

// SetDefaultLocalLabel names the local machine only while its label is
// unset (empty, migration 0022); a name the user gave is kept. It reports
// whether it set the label.
func (s *Store) SetDefaultLocalLabel(ctx context.Context, label string) (bool, error) {
	label, err := machineLabel(label, false)
	if err != nil {
		return false, err
	}
	res, err := s.pool.Exec(ctx, `UPDATE machines SET label=$2 WHERE id=$1 AND label=''`, LocalMachine, label)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() > 0, nil
}

// RenameMachine sets a machine's label.
func (s *Store) RenameMachine(ctx context.Context, id int64, label string) error {
	label, err := machineLabel(label, false)
	if err != nil {
		return err
	}
	res, err := s.pool.Exec(ctx, `UPDATE machines SET label=$2 WHERE id=$1 AND removed_at IS NULL`, id, label)
	if err == nil && res.RowsAffected() == 0 {
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
	err := s.inTx(ctx, func(tx *Tx) error {
		res, err := tx.Exec(ctx, `UPDATE machines SET removed_at=stage_now() WHERE id=$1 AND removed_at IS NULL`, id)
		if err != nil {
			return err
		}
		if res.RowsAffected() == 0 {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM machine_tokens WHERE machine_id=$1`, id); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM harness_connections WHERE machine_id=$1 AND active=1`, id)
		if err != nil {
			return err
		}
		ids, err := collectRows(rows, rowTo[int64])
		if err != nil {
			return err
		}
		for _, c := range ids {
			referenced, err := connectionReferenced(ctx, tx, c)
			if err != nil {
				return err
			}
			if !referenced {
				if err := deleteConnection(ctx, tx, c); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE harness_connections SET active=0,retired_at=stage_now() WHERE id=$1`, c); err != nil {
				return err
			}
		}
		return nil
	})
	s.dropConnections()
	return err
}

// connectionReferenced: relations (hasRelations) or unlaunched tasks and
// schedules point at the connection, so it must stay as history.
func connectionReferenced(ctx context.Context, tx *Tx, id int64) (bool, error) {
	related, err := hasRelations(ctx, tx, id)
	if err != nil || related {
		return related, err
	}
	var pending bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE connection_id=$1) OR EXISTS(SELECT 1 FROM schedules WHERE connection_id=$1)`, id).Scan(&pending)
	return pending, err
}
