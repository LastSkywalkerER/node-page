package update

import (
	"context"
	"fmt"
	"os"
)

// RunCLI implements the `node-stats update [--check]` subcommand for native
// installs: check GitHub for a newer release and (unless checkOnly) download +
// self-replace the binary. Exit codes: 0 up-to-date/updated, 1 error, 10
// update-available (checkOnly) for cron/timer use.
func RunCLI(checkOnly bool) {
	svc := NewService("", "", false, nil, nil)
	ctx := context.Background()

	if err := svc.Check(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "update check failed:", err)
		os.Exit(1)
	}
	info := svc.Status()
	if !info.UpdateAvailable {
		latest := info.Latest
		if latest == "" {
			latest = "unknown"
		}
		fmt.Printf("node-stats %s is up to date (latest release: %s)\n", info.Current, latest)
		return
	}

	fmt.Printf("Update available: %s → %s\n", info.Current, info.Latest)
	if checkOnly {
		os.Exit(10)
	}
	msg, err := svc.UpdateNow(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "update failed:", err)
		os.Exit(1)
	}
	fmt.Println(msg)
}
