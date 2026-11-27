package store

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// Actor identifies who performed a Stage operation. Personal access has no
// verified person. An agent's credential account is distinct from an initiator.
type Actor struct {
	Kind                string `json:"kind"`
	UserID              int64  `json:"userId,omitempty"`
	InitiatorUserID     int64  `json:"initiatorUserId,omitempty"`
	CredentialAccountID int64  `json:"credentialAccountId,omitempty"`
	DisplayName         string `json:"displayName,omitempty"`
}

// Cause relates an automatic action to the work that produced it.
type Cause struct {
	Kind         string      `json:"kind"`
	TaskID       int64       `json:"taskId,omitempty"`
	AcceptanceID int64       `json:"acceptanceId,omitempty"`
	ScheduleID   int64       `json:"scheduleId,omitempty"`
	Attempt      int         `json:"attempt,omitempty"`
	Session      *SessionRef `json:"session,omitempty"`
	RequestID    string      `json:"requestId,omitempty"`
}

// Attribution is provenance only. A NULL stamp represents historical data.
// DisplayName is populated from the public user record at read time.
type Attribution struct {
	Actor         Actor  `json:"actor"`
	Cause         *Cause `json:"cause,omitempty"`
	SourceMachine *int64 `json:"sourceMachineId,omitempty"`
}

func (a Attribution) Value() (driver.Value, error) {
	a.Actor.DisplayName = ""
	if a.Actor.Kind == "" {
		a.Actor.Kind = "unknown"
	}
	b, err := json.Marshal(a)
	return string(b), err
}

func (a *Attribution) Scan(value any) error {
	if value == nil {
		*a = Attribution{Actor: Actor{Kind: "historical"}}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return fmt.Errorf("scan attribution: unexpected %T", value)
	}
	return json.Unmarshal(b, a)
}
