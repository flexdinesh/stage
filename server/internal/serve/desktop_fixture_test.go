package serve_test

import (
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/flexdinesh/stage/server/internal/serve"
)

// TestDesktopSharedServerProcess is launched by the desktop integration
// suite while shared access is an internal runtime option.
func TestDesktopSharedServerProcess(t *testing.T) {
	addr := os.Getenv("STAGE_DESKTOP_TEST_ADDR")
	if addr == "" {
		t.Skip("desktop integration subprocess")
	}
	dir := isolate(t)
	c := cfg(t, filepath.Join(dir, "data"), addr, "unix:"+filepath.Join(dir, "product.sock"))
	c.PublicURL = "http://" + addr
	ctx, stop := signal.NotifyContext(t.Context(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	shared := true
	if err := serve.Run(ctx, serve.Options{
		Config: c, SharedAccess: &shared, BootstrapOutput: os.Stdout,
		Ready: func(r serve.Ready) {
			_, _ = os.Stdout.WriteString("DESKTOP_TEST_SOCKET=" + r.Listeners[1].Address + "\n")
		},
	}); err != nil {
		t.Fatal(err)
	}
}
