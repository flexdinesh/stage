// Command stagestub serves the Stage contract stub (package stub) on a
// Unix domain socket only, for the desktop fixture's checks.
//
//	stagestub -socket /tmp/stage-fixture/stage.sock
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/flexdinesh/stage/desktop/fixture/stub"
)

func main() {
	socket := flag.String("socket", "", "Unix socket path to listen on (required)")
	flag.Parse()
	if *socket == "" {
		log.Fatal("stagestub: -socket is required")
	}
	ln, err := stub.Listen(*socket)
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Handler: stub.New().Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("stagestub: listening on unix:%s", *socket)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
