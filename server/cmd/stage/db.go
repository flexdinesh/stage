package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db/datacopy"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/serve"
)

const dbUsage = `Usage:
  stage db copy --from PATH --to URL
      copy the SQLite database at PATH (a stopped server's <data dir>/stage.db)
      into the empty Postgres database at URL (postgres:// or postgresql://),
      then verify every table's row count and checksum. Needs a binary with
      Postgres support (the Docker image). See docs/storage.md "Data copy and cutover".
  stage db invalidate-access
      clear restored sessions and account links before restarting the server.
      Set STAGE_DATA_DIR explicitly. For Postgres, also set STAGE_DATABASE_URL.

Exit: 0 success, 2 usage, 75 the data dir or database is held by a server,
78 incompatible schema or invalid configuration, 1 failed or refused.
`

// copyStartTimeout bounds opening the target and taking its ownership lock.
const copyStartTimeout = 30 * time.Second

// errNoPostgresCopy refuses `stage db copy` in a binary without the
// postgres tag (exit 78).
var errNoPostgresCopy = errors.New("this stage binary has no Postgres support: run stage db copy from the Docker image")

// runDB is `stage db ...`: it prints errors to stderr and returns the exit
// code (exitCode's mapping).
func runDB(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	err := dbCommand(ctx, args, stdout)
	code := exitCode(err)
	if err != nil && code != 0 {
		fmt.Fprintf(stderr, "stage db: %v\n", err)
		if code == 2 {
			fmt.Fprintf(stderr, "\n%s", dbUsage)
		}
	}
	return code
}

func dbCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errUsage{"missing command"}
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, dbUsage)
		return nil
	case "copy":
		return dbCopy(ctx, args[1:], stdout)
	case "invalidate-access":
		return dbInvalidateAccess(ctx, args[1:], stdout)
	}
	return errUsage{fmt.Sprintf("unknown command %q", args[0])}
}

func dbInvalidateAccess(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(stdout, dbUsage)
		return nil
	}
	if len(args) != 0 {
		return errUsage{"invalidate-access takes no arguments"}
	}
	if os.Getenv("STAGE_DATA_DIR") == "" {
		return config.Invalid(errors.New("Set STAGE_DATA_DIR to the restored installation's data directory."))
	}
	dataDir, err := config.DataDirFrom(os.Getenv)
	if err != nil {
		return config.Invalid(err)
	}
	database, err := config.ParseDatabase(os.Getenv(config.DatabaseEnv))
	if err != nil {
		return config.Invalid(err)
	}
	cfg := config.Config{DataDir: dataDir, Database: database}
	if err := serve.InvalidateRestoredAccess(ctx, cfg); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Restored sessions and account links cleared.")
	return err
}

// dbCopy is `stage db copy --from PATH --to URL` (docs/storage.md "Data
// copy and cutover"). It never prints the URL.
func dbCopy(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("db copy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	from := fs.String("from", "", "the SQLite database file")
	to := fs.String("to", "", "the Postgres database URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, dbUsage)
			return nil
		}
		return errUsage{"copy: " + err.Error()}
	}
	switch {
	case fs.NArg() > 0:
		return errUsage{fmt.Sprintf("copy: unexpected argument %q", fs.Arg(0))}
	case *from == "":
		return errUsage{"copy: --from is required"}
	case *to == "":
		return errUsage{"copy: --to is required"}
	}
	target, err := config.ParseDatabase(*to)
	switch {
	case errors.Is(err, config.ErrNoPostgres):
		return config.Invalid(errNoPostgresCopy)
	case err != nil || target.Engine() != config.EnginePostgres:
		return config.Invalid(errors.New("copy: --to wants a postgres:// or postgresql:// URL"))
	}
	path, err := filepath.Abs(*from)
	if err != nil {
		return err
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("source database: %w", err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("source database %s is not a file", path)
	}
	// Offline: hold the data dir's lock for the whole copy, so no server
	// runs on the source meanwhile.
	owner, err := instance.Lock(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer owner.Release()
	src, err := sqlite.OpenCopySource(ctx, path)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, closeDst, err := openCopyTarget(ctx, target.URL)
	if err != nil {
		return err
	}
	defer closeDst()
	rep, err := datacopy.Run(ctx, src, dst)
	if err != nil {
		return err
	}
	migrated := ""
	if rep.Migrated {
		migrated = ", target migrated"
	}
	var rows int64
	for _, t := range rep.Tables {
		rows += t.Rows
	}
	fmt.Fprintf(stdout, "copied %s to Postgres: schema %d%s, %d tables, %d rows, verified\n", path, rep.Version, migrated, len(rep.Tables), rows)
	for _, t := range rep.Tables {
		fmt.Fprintf(stdout, "  %-28s %9d rows  %s  ok\n", t.Name, t.Rows, t.Checksum[:16])
	}
	return nil
}
