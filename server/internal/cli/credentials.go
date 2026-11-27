package cli

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/listen"
)

const agentCredentialEnv = "STAGE_AGENT_CREDENTIAL_FILE"

func (c *cmd) resolution() client.Resolution {
	if c.human {
		return client.Resolve(c.url, c.env.Getenv)
	}
	return client.ResolveAgent(c.url, c.env.Getenv)
}

func (c *cmd) apiClient() *client.Client {
	res := c.resolution()
	cl := client.New(res.URL)
	if c.human {
		path, err := sessionPath(c.env, res.URL)
		return cl.WithCredential(func() (client.Credential, error) {
			return requiredHuman(path, err)
		})
	}
	if path := c.env.Getenv(agentCredentialEnv); path != "" && !strings.HasPrefix(res.URL, "unix:") {
		return cl.WithCredential(func() (client.Credential, error) {
			return readCredential(path, store.SessionAgent)
		})
	}
	return cl
}

func requiredHuman(path string, pathErr error) (client.Credential, error) {
	if pathErr != nil {
		return client.Credential{}, pathErr
	}
	cred, err := readCredential(path, store.SessionCLI)
	if errors.Is(err, fs.ErrNotExist) {
		return client.Credential{}, errors.New("no CLI session saved for this address. Run stage auth login with the same --url")
	}
	return cred, err
}

func sessionPath(env Env, rawURL string) (string, error) {
	target, err := client.CredentialTarget(rawURL)
	if err != nil {
		return "", err
	}
	cfg := env.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(cfg) {
		if !filepath.IsAbs(env.Home) {
			return "", errors.New("set HOME or XDG_CONFIG_HOME to an absolute path for Stage CLI sessions")
		}
		cfg = filepath.Join(env.Home, ".config")
	}
	sum := sha256.Sum256([]byte(target))
	return filepath.Join(cfg, buildinfo.Identity, "cli", fmt.Sprintf("%x.json", sum)), nil
}

func credentialFileInfo(info fs.FileInfo, dir bool) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Geteuid() || info.Mode().Perm()&0o077 != 0 {
		return false
	}
	if dir {
		return info.IsDir()
	}
	return info.Mode().IsRegular()
}

func readCredential(path string, audience store.SessionAudience) (client.Credential, error) {
	var cred client.Credential
	dir, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return cred, err
	}
	if !credentialFileInfo(dir, true) {
		return cred, errors.New("Stage credentials need a directory owned by you with mode 0700")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return cred, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return cred, err
	}
	if !credentialFileInfo(info, false) || info.Size() > 8192 {
		return cred, errors.New("Stage credentials need a regular file owned by you with mode 0600")
	}
	b, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil {
		return cred, err
	}
	if len(b) > 8192 || json.Unmarshal(b, &cred) != nil {
		return cred, errors.New("invalid Stage credential file")
	}
	return cred, cred.Validate(audience)
}

// prepareCredential checks the existing destination before any server-side
// rotation. An existing file must contain the intended credential type.
func prepareCredential(path string, audience store.SessionAudience) error {
	if err := listen.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	_, err := readCredential(path, audience)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func writeCredential(path string, cred client.Credential) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".stage-credential-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := json.NewEncoder(f).Encode(cred); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
