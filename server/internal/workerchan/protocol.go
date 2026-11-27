// Package workerchan is the worker channel (docs/runtime.md "Worker
// channel", ADR 0002 section 4): the main server's RPC link to a remote
// machine's `stage worker`. The worker always dials out: one long-lived SSE
// stream carries requests and cancels down (GET /api/worker/channel), and
// one POST per message carries results, heartbeats and worker-initiated
// calls up (POST /api/worker/channel). Standard library only.
//
// This file is the wire protocol both sides share; hub.go is the main
// server's multiplexer.
package workerchan

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Message kinds.
const (
	// KindPing is a liveness round trip, in either direction. Its result
	// body is a Pong.
	KindPing = "ping"
	// KindCancel (main -> worker) cancels the call with the same id; best
	// effort.
	KindCancel = "cancel"
	// KindHeartbeat (worker -> main) proves the worker is alive; no reply.
	KindHeartbeat = "heartbeat"
	// KindResult answers the request with the same id: Body or Error.
	KindResult = "result"
	// KindReady publishes the worker's full capability snapshot for this session.
	KindReady = "ready"
)

// GatewayPath is the worker route a worker's agent gateway forwards local
// /api and /mcp requests under: GatewayPath+"/api/..." and
// GatewayPath+"/mcp" (docs/runtime.md "Agent gateway").
const GatewayPath = "/api/worker/gateway"

// Headers.
const (
	// SessionHeader names the channel session on an up POST (the hello
	// event's session).
	SessionHeader     = "Stage-Channel-Session"
	ProtocolMinHeader = "Stage-Channel-Protocol-Min"
	ProtocolMaxHeader = "Stage-Channel-Protocol-Max"
	// VersionHeader carries the worker's build version (buildinfo.Version)
	// on the stream request and join.
	VersionHeader = "Stage-Version"
	// GatewayHeader lists the worker's agent gateway listener URLs
	// (comma-separated, listen.Addr.URL form) on the stream request.
	GatewayHeader = "Stage-Worker-Gateway"
)

// SSE event names on the down stream.
const (
	EventHello = "hello"
	EventRPC   = "rpc"
)

// Default intervals (ADR 0002): each side speaks about every 15s, and 45s
// of silence ends the channel.
const (
	DefaultKeepalive = 15 * time.Second
	DefaultTimeout   = 45 * time.Second
)

// Envelope is one channel message, in both directions.
type Envelope struct {
	// ID matches a result to its request; unique within a channel session.
	ID   string `json:"id,omitempty"`
	Kind string `json:"kind"`
	// Deadline is when the caller stops waiting (absolute UTC). The worker
	// runs the call under a context with this deadline.
	Deadline *time.Time `json:"deadline,omitempty"`
	// ConnectionID names the harness connection the call is for (0: the
	// machine itself).
	ConnectionID int64 `json:"connectionId,omitempty"`
	// Call names the main server's pending request a worker-initiated call
	// belongs to (a callback while that request runs, Hub.CallWith).
	Call  string          `json:"call,omitempty"`
	Body  json.RawMessage `json:"body,omitempty"`
	Error *Error          `json:"error,omitempty"`
}

// Error is a failed call's result.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error codes in results.
const (
	CodeUnknownKind          = "unknown_kind"
	CodeCancelled            = "cancelled"
	CodeTimeout              = "timeout"
	CodeInternal             = "internal"
	CodeProtocolMismatch     = "channel_protocol_mismatch"
	CodeOperationUnavailable = "operation_unavailable"
	// CodeCallEnded: a callback named a request that no longer waits (it
	// returned, timed out or its stream ended).
	CodeCallEnded = "call_ended"
)

// ProtocolVersion is the current worker channel contract, independent of the API.
const ProtocolVersion = 3

type VersionRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

func CurrentRange() VersionRange { return VersionRange{Min: ProtocolVersion, Max: ProtocolVersion} }

func (r VersionRange) Valid() bool { return r.Min >= 1 && r.Max >= r.Min }

// Select chooses the highest shared version. Both ranges must be valid.
func (r VersionRange) Select(peer VersionRange) (int, bool) {
	v := min(r.Max, peer.Max)
	return v, r.Valid() && peer.Valid() && v >= max(r.Min, peer.Min)
}

// ParseRange validates the two opening request headers.
func ParseRange(lo, hi string) (VersionRange, bool) {
	lower, loErr := strconv.Atoi(lo)
	upper, hiErr := strconv.Atoi(hi)
	r := VersionRange{Min: lower, Max: upper}
	return r, loErr == nil && hiErr == nil && r.Valid()
}

func UpdateReason(serverOlder bool) string {
	if serverOlder {
		return "Update Stage on the main machine. Its worker channel protocol is older than this worker's."
	}
	return "Update Stage on the worker machine. Its worker channel protocol is older than the main machine's."
}

// Hello is the first event of a stream.
type Hello struct {
	Session         string       `json:"session"`
	MachineID       int64        `json:"machineId"`
	ProtocolRange   VersionRange `json:"protocolRange"`
	ProtocolVersion int          `json:"protocolVersion"`
	Kinds           []string     `json:"kinds"`
	ChannelKinds    []string     `json:"channelKinds"`
	// HeartbeatSec is how often the worker should POST a heartbeat.
	HeartbeatSec int `json:"heartbeatSec"`
	// TimeoutSec is the silence after which either side ends the channel.
	TimeoutSec int `json:"timeoutSec"`
}

// Ready replaces a session's capabilities, including the traits of each connection.
// Details carries placement metadata handled by the worker host.
type Ready struct {
	Kinds        []string        `json:"kinds"`
	ChannelKinds []string        `json:"channelKinds"`
	UpKinds      []string        `json:"upKinds,omitempty"` // independent worker-initiated capabilities
	Connections  []Connection    `json:"connections"`
	Details      json.RawMessage `json:"details,omitempty"`
}

type Connection struct {
	ConnectionID int64    `json:"connectionId"`
	Traits       []string `json:"traits"`
}

// Pong is a ping result.
type Pong struct {
	Version string    `json:"version"`
	Time    time.Time `json:"time"`
}

// RemoteError is a call the other side answered with an error.
type RemoteError struct{ Code, Message string }

func (e *RemoteError) Error() string {
	return fmt.Sprintf("worker call failed (%s): %s", e.Code, e.Message)
}

var (
	// ErrOffline: the machine has no live channel. The call was not sent.
	ErrOffline     = errors.New("machine offline: its worker is not connected")
	ErrNegotiating = errors.New("the Stage worker is negotiating its capabilities")
	// ErrLostReply: the call was sent but no reply arrived before its
	// deadline or the stream ended. The outcome is unknown, never failure:
	// callers reconcile.
	ErrLostReply    = errors.New("worker reply lost: outcome unknown")
	ErrReplyTimeout = fmt.Errorf("%w: deadline elapsed", ErrLostReply)
	// ErrReplaced: an up POST named a channel session that a newer stream
	// replaced or that ended.
	ErrReplaced = errors.New("channel session replaced or ended")
)

// WriteEvent writes one SSE event whose data is v as JSON (one line).
func WriteEvent(w io.Writer, event string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	return err
}

// WriteComment writes an SSE comment (a keepalive).
func WriteComment(w io.Writer, text string) error {
	_, err := fmt.Fprintf(w, ": %s\n\n", text)
	return err
}

// Event is one SSE event read from a stream.
type Event struct {
	Name string
	Data []byte
}

// Reader reads SSE events. Comments are reported through OnLine (any line
// at all counts as liveness) and otherwise skipped.
type Reader struct {
	r *bufio.Reader
	// OnLine is called for every line read, comments included.
	OnLine func()
}

// maxEventBytes bounds one event (the API's body cap is 32 MiB; channel
// messages are far smaller).
const maxEventBytes = 32 << 20

// NewReader reads events from r.
func NewReader(r io.Reader) *Reader { return &Reader{r: bufio.NewReaderSize(r, 64<<10)} }

// Next is the next event (io.EOF when the stream ends cleanly).
func (rd *Reader) Next() (Event, error) {
	var ev Event
	var data strings.Builder
	has := false
	for {
		line, err := rd.r.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && line == "" {
				return Event{}, io.EOF
			}
			if errors.Is(err, io.EOF) {
				return Event{}, io.ErrUnexpectedEOF
			}
			return Event{}, err
		}
		if rd.OnLine != nil {
			rd.OnLine()
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if has {
				ev.Data = []byte(data.String())
				return ev, nil
			}
			ev = Event{}
		case strings.HasPrefix(line, ":"):
		default:
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				ev.Name, has = value, true
			case "data":
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(value)
				if data.Len() > maxEventBytes {
					return Event{}, fmt.Errorf("event exceeds %d bytes", maxEventBytes)
				}
				has = true
			}
		}
	}
}

// Upcall sends a worker-initiated call that belongs to the request being
// handled (Envelope.Call) and returns its reply body; an error reply is a
// *RemoteError.
type Upcall func(ctx context.Context, kind string, body any) (json.RawMessage, error)

// WorkerHandler answers one of the main server's calls on the worker. A
// *RemoteError keeps its code in the result.
type WorkerHandler func(ctx context.Context, env Envelope, up Upcall) (any, error)

// RefreshCapabilities is a handler result that changed the worker's setup.
// The runner sends a new ready snapshot before returning Value to the server.
type RefreshCapabilities struct{ Value any }
