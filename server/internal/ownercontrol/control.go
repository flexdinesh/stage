// Package ownercontrol serves installation-owner operations over a private
// Unix socket. Its protocol is separate from Stage's product listeners.
package ownercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/auth"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/principal"
)

const (
	setupReissue = "reissue-setup"
	adminRecover = "recover-admin"
	deadline     = 10 * time.Second
	messageLimit = 4 << 10
)

var errUnavailable = errors.New("Cannot reach the private owner socket. Run this command on the server as the Stage installation owner, with the server's STAGE_DATA_DIR.")

// Operations are the only actions available over the owner socket.
type Operations interface {
	ReissueSetup(context.Context) (auth.Link, error)
	RecoverAdmin(context.Context, string) (auth.Link, error)
}

type request struct {
	Action string `json:"action"`
	Email  string `json:"email,omitempty"`
}

type response struct {
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
}

type Server struct {
	listener *listen.Listener
	dataDir  string
	ops      Operations
}

func ownerDir(dataDir string) string { return filepath.Join(dataDir, "owner") }

func socketPath(dataDir string) string { return filepath.Join(ownerDir(dataDir), "control.sock") }

// BootstrapPath is the private startup output for managed and unattended
// servers. It is neither a log nor part of the runtime holder record.
func BootstrapPath(dataDir string) string { return filepath.Join(ownerDir(dataDir), "bootstrap.txt") }

func tempBootstrapPath(dataDir string) string {
	return filepath.Join(ownerDir(dataDir), ".bootstrap.tmp")
}

func Open(dataDir string, ops Operations) (*Server, error) {
	if err := listen.EnsurePrivateDir(dataDir); err != nil {
		return nil, err
	}
	address := listen.Addr{Network: "unix", Address: socketPath(dataDir), Private: true}
	listener, err := address.Listen()
	if err != nil {
		return nil, err
	}
	return &Server{listener: listener, dataDir: dataDir, ops: ops}, nil
}

func (s *Server) Close() error { return s.listener.Close() }

func (s *Server) Serve(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	var workers sync.WaitGroup
	defer workers.Wait()
	active := make(chan struct{}, 4)
	for {
		conn, err := s.listener.Accept()
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case active <- struct{}{}:
			workers.Go(func() {
				defer func() { <-active }()
				s.serveOne(ctx, conn)
			})
		default:
			_ = conn.Close()
		}
	}
}

func (s *Server) serveOne(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	ctx = principal.With(ctx, principal.Actor{Kind: principal.Owner})
	_ = conn.SetDeadline(time.Now().Add(deadline))
	d := json.NewDecoder(io.LimitReader(conn, messageLimit))
	d.DisallowUnknownFields()
	var req request
	if err := d.Decode(&req); err != nil {
		return
	}
	var link auth.Link
	var err error
	switch req.Action {
	case setupReissue:
		link, err = s.ops.ReissueSetup(ctx)
		if err == nil {
			// Rotation has already invalidated the old link in storage.
			_ = s.DiscardBootstrap()
		}
	case adminRecover:
		link, err = s.ops.RecoverAdmin(ctx, req.Email)
	default:
		return
	}
	reply := response{URL: link.URL()}
	if err != nil {
		reply = response{Error: auth.PublicMessage(err)}
	}
	_ = json.NewEncoder(conn).Encode(reply)
}

// PublishBootstrap replaces the private output atomically. The file is
// created 0600 in the verified owner's directory before content is written.
func (s *Server) PublishBootstrap(output string) error {
	path := tempBootstrapPath(s.dataDir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(path)
	_, writeErr := f.WriteString(output)
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return err
	}
	return os.Rename(path, BootstrapPath(s.dataDir))
}

func (s *Server) DiscardBootstrap() error {
	for _, path := range []string{BootstrapPath(s.dataDir), tempBootstrapPath(s.dataDir)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func ReissueSetup(ctx context.Context, dataDir string) (string, error) {
	return call(ctx, dataDir, request{Action: setupReissue})
}

func RecoverAdmin(ctx context.Context, dataDir, email string) (string, error) {
	return call(ctx, dataDir, request{Action: adminRecover, Email: email})
}

func call(ctx context.Context, dataDir string, req request) (string, error) {
	path := socketPath(dataDir)
	st, err := os.Lstat(path)
	if err != nil || st.Mode()&os.ModeSocket == 0 || st.Mode().Perm() != 0o600 ||
		listen.EnsurePrivateDir(dataDir) != nil || listen.EnsurePrivateDir(ownerDir(dataDir)) != nil {
		return "", errUnavailable
	}
	dial := net.Dialer{Timeout: deadline}
	conn, err := dial.DialContext(ctx, "unix", path)
	if err != nil {
		return "", errUnavailable
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetDeadline(time.Now().Add(deadline))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return "", errUnavailable
	}
	var reply response
	if err := json.NewDecoder(io.LimitReader(conn, messageLimit)).Decode(&reply); err != nil {
		return "", errUnavailable
	}
	if reply.Error != "" {
		return "", errors.New(reply.Error)
	}
	if reply.URL == "" {
		return "", errUnavailable
	}
	return reply.URL, nil
}
