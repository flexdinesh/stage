package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/filelock"
)

func runAuth(ctx context.Context, env Env, args []string) error {
	action, args := verb(args, "")
	if action == "agent" {
		action, args = verb(args, "")
		if action != "rotate" && action != "revoke" {
			return usagef("auth agent: use rotate or revoke")
		}
	} else if action != "login" && action != "logout" && action != "whoami" {
		return usagef("auth: use login, logout, whoami, or agent")
	}
	c := newCmd(env, "auth "+action)
	var email, output, networkTarget string
	switch action {
	case "login":
		c.fs.StringVar(&email, "email", "", "account email")
	case "rotate":
		c.fs.StringVar(&output, "out", "", "private file for the agent credential")
		c.fs.StringVar(&networkTarget, "target", "", "Stage address the agent will use")
	}
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.pos) != 0 {
		return usagef("auth %s: unexpected argument", action)
	}
	raw := client.Resolve(c.url, env.Getenv).URL
	target, err := client.CredentialTarget(raw)
	if err != nil {
		return errors.New("choose a Stage address using HTTPS, loopback HTTP, or an absolute Unix socket")
	}
	path, err := sessionPath(env, target)
	if err != nil {
		return err
	}
	cl := client.New(target)
	if action == "login" {
		if strings.TrimSpace(email) == "" {
			return usagef("auth login: pass --email EMAIL")
		}
		return login(ctx, env, cl, path, strings.TrimSpace(email))
	}
	if action == "logout" {
		return logout(ctx, env, cl, path)
	}
	cred, err := requiredHuman(path, nil)
	if err != nil {
		return err
	}
	if cred.Target != target {
		return errors.New("saved CLI session is for a different Stage address")
	}
	human := cl.WithCredential(func() (client.Credential, error) { return cred, nil })
	switch action {
	case "whoami":
		account, err := human.Me(ctx)
		if err != nil {
			return err
		}
		return c.out(account, func(w io.Writer) {
			if account.Email != nil {
				fmt.Fprintf(w, "%s (%s) at %s\n", *account.Email, account.Role, target)
			}
		})
	case "revoke":
		if err := human.RevokeAgent(ctx); err != nil {
			return err
		}
		fmt.Fprintln(env.Stdout, "Revoked your Stage agent credential.")
		return nil
	default:
		if output == "" {
			return usagef("auth agent rotate: pass --out FILE in a private directory")
		}
		if networkTarget == "" {
			networkTarget = target
		}
		return rotateAgent(ctx, env, human, cred, output, networkTarget)
	}
}

func login(ctx context.Context, env Env, c *client.Client, path, email string) error {
	status, err := c.AccessStatus(ctx)
	if err != nil {
		return fmt.Errorf("connect to Stage at %s: %w", c.BaseURL, err)
	}
	switch {
	case status.AgentOnly:
		return errors.New("use the Stage product address to sign in. The selected socket is for agents")
	case status.Mode == string(store.AccessPending):
		return errors.New("the Stage installation owner must finish setup before you can sign in")
	case status.Mode != string(store.AccessClaimed) || status.InstallationID == "":
		return errors.New("this Stage server has no active shared accounts")
	}
	password, err := hiddenPassword(env)
	if err != nil {
		return err
	}
	defer clear(password)
	if err := prepareCredential(path, store.SessionCLI); err != nil {
		return err
	}
	lock, err := filelock.Acquire(path+".lock", 0o600)
	if err != nil {
		return err
	}
	defer lock.Release()
	previous, err := readCredential(path, store.SessionCLI)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	cred, err := c.Login(ctx, status.InstallationID, email, string(password))
	if err != nil {
		return err
	}
	if previous.InstallationID == cred.InstallationID && previous.Target == cred.Target {
		if err := revokeSession(ctx, c, previous); err != nil {
			return errors.Join(err, revokeSession(ctx, c, cred))
		}
	}
	if err := writeCredential(path, cred); err != nil {
		return errors.Join(err, revokeSession(ctx, c, cred))
	}
	fmt.Fprintf(env.Stdout, "Signed in to %s. Use --human for CLI commands that need your account.\n", c.BaseURL)
	return nil
}

func hiddenPassword(env Env) ([]byte, error) {
	read := env.ReadPassword
	if read == nil {
		f, ok := env.Stdin.(*os.File)
		if !ok || !term.IsTerminal(f.Fd()) {
			return nil, errors.New("sign-in needs a terminal to read your password")
		}
		read = func() ([]byte, error) { return term.ReadPassword(f.Fd()) }
	}
	fmt.Fprint(env.Stderr, "Password: ")
	password, err := read()
	fmt.Fprintln(env.Stderr)
	return password, err
}

func logout(ctx context.Context, env Env, c *client.Client, path string) error {
	if _, err := readCredential(path, store.SessionCLI); errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(env.Stdout, "No CLI session saved for %s.\n", c.BaseURL)
		return nil
	} else if err != nil {
		return err
	}
	lock, err := filelock.Acquire(path+".lock", 0o600)
	if err != nil {
		return err
	}
	defer lock.Release()
	cred, err := readCredential(path, store.SessionCLI)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err == nil {
		if err := revokeSession(ctx, c, cred); err != nil {
			return fmt.Errorf("could not revoke the CLI session. Retry logout with the same Stage address: %w", err)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	fmt.Fprintf(env.Stdout, "Signed out of %s.\n", c.BaseURL)
	return nil
}

func revokeSession(ctx context.Context, c *client.Client, cred client.Credential) error {
	err := c.WithCredential(func() (client.Credential, error) { return cred, nil }).Logout(ctx)
	if client.IsStatus(err, http.StatusUnauthorized) {
		return nil // already expired or revoked
	}
	return err
}

func rotateAgent(ctx context.Context, env Env, human *client.Client, session client.Credential, path, raw string) error {
	target, err := client.CredentialTarget(raw)
	if err != nil || strings.HasPrefix(target, "unix:") {
		return errors.New("select the agent's HTTPS or loopback HTTP address with --target. Local agents can use the private agent socket")
	}
	status, err := client.New(target).AccessStatus(ctx)
	if err != nil {
		return fmt.Errorf("check the agent target: %w", err)
	}
	if status.AgentOnly || status.Mode != string(store.AccessClaimed) || status.InstallationID != session.InstallationID {
		return errors.New("the agent target must reach the same shared Stage installation as your CLI session")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := prepareCredential(path, store.SessionAgent); err != nil {
		return err
	}
	lock, err := filelock.Acquire(path+".lock", 0o600)
	if err != nil {
		return err
	}
	defer lock.Release()
	cred, err := human.RotateAgent(ctx)
	if err != nil {
		return err
	}
	if cred.InstallationID != session.InstallationID {
		return errors.New("Stage installation changed during rotation")
	}
	cred.Target = target
	if err := writeCredential(path, cred); err != nil {
		return fmt.Errorf("Stage rotated the credential but could not save it. Fix the file and rotate again: %w", err)
	}
	fmt.Fprintf(env.Stdout, "Saved agent credential for %s to %s. Expires %s.\n", target, path, cred.ExpiresAt.UTC().Format(time.RFC3339))
	return nil
}
