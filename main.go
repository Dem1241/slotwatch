// slotwatch: watch PostgreSQL replication slots in your terminal, and catch
// the one quietly filling your disk with WAL.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Dem1241/slotwatch/internal/check"
	"github.com/Dem1241/slotwatch/internal/demo"
	"github.com/Dem1241/slotwatch/internal/pg"
	"github.com/Dem1241/slotwatch/internal/slots"
	"github.com/Dem1241/slotwatch/internal/ui"
)

var version = "dev"

func main() {
	code, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "slotwatch:", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func run() (int, error) {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "connection string (env DATABASE_URL), otherwise PGHOST, PGUSER and friends like psql")
	flag.StringVar(dsn, "d", *dsn, "shorthand for -dsn")
	demoMode := flag.Bool("demo", false, "watch a pretend server with a stuck debezium slot")
	interval := flag.Duration("interval", 2*time.Second, "how often to look at the slots")
	warn := flag.String("warn", "1GB", "retained WAL worth a warning")
	danger := flag.String("danger", "10GB", "retained WAL worth paging someone")
	inactive := flag.Duration("inactive", 10*time.Minute, "how long a slot may be unused before it's a warning")
	checkMode := flag.Bool("check", false, "print a report and exit 0 ok, 1 warn, 2 danger, 3 unknown")
	jsonOut := flag.Bool("json", false, "with -check, print the report as json")
	showVersion := flag.Bool("version", false, "print the version")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: slotwatch [flags]\n\nWatch PostgreSQL replication slots and catch the one filling your disk with WAL.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("slotwatch", buildVersion())
		return 0, nil
	}
	if flag.NArg() > 0 {
		flag.Usage()
		return 2, nil
	}

	limits := slots.Limits{Inactive: *inactive}
	var err error
	if limits.Warn, err = slots.ParseBytes(*warn); err != nil {
		return 2, fmt.Errorf("-warn: %w", err)
	}
	if limits.Danger, err = slots.ParseBytes(*danger); err != nil {
		return 2, fmt.Errorf("-danger: %w", err)
	}

	var src ui.Source
	if *demoMode {
		src = demo.New(time.Now())
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client, err := pg.Connect(ctx, *dsn)
		if err != nil {
			return check.ExitUnknown, fmt.Errorf("%w\n(no database handy? try: slotwatch --demo)", err)
		}
		defer client.Close()
		src = client
	}

	if *checkMode {
		return runCheck(src, limits, *jsonOut)
	}
	_, err = tea.NewProgram(ui.New(src, limits, *interval)).Run()
	return 0, err
}

// runCheck looks at the slots once and reports.
func runCheck(src ui.Source, limits slots.Limits, asJSON bool) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snap, err := src.Snapshot(ctx)
	if err != nil {
		return check.ExitUnknown, err
	}
	sts := slots.NewTracker(limits).Update(snap)
	if asJSON {
		err = check.JSON(os.Stdout, snap, sts)
	} else {
		err = check.Text(os.Stdout, snap, sts)
	}
	if err != nil {
		return check.ExitUnknown, err
	}
	return check.Code(sts), nil
}

// buildVersion falls back to the module version for go install builds, which
// don't get the release ldflags.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}
