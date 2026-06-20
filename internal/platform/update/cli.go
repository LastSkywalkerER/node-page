package update

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// RunCLI implements the `node-stats update [--check]` subcommand for native
// installs: check GitHub for a newer release and (unless checkOnly) download +
// self-replace the binary. Exit codes: 0 up-to-date/updated, 1 error, 10
// update-available (checkOnly) for cron/timer use.
func RunCLI(checkOnly bool) {
	// Native installs persist the chosen release channel (and any repo override)
	// to the .env the server reads at boot. Load it here so `node-stats update`
	// follows the SAME channel the admin picked in the UI instead of always
	// updating on stable. Run from the install/data dir (or set NODE_STATS_ENV_FILE)
	// so this resolves the right file.
	_ = godotenv.Load(envFilePath())

	repo := strings.TrimSpace(os.Getenv("NODE_STATS_REPO"))
	channel := os.Getenv("NODE_STATS_RELEASE_CHANNEL")
	svc := NewService(repo, "", false, nil, nil).WithReleaseChannel(channel, nil)
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
		fmt.Printf("node-stats %s is up to date (%s channel; latest: %s)\n", info.Current, info.Channel, latest)
		return
	}

	fmt.Printf("Update available (%s channel): %s → %s\n", info.Channel, info.Current, info.Latest)
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

// envFilePath mirrors config.EnvFilePath without importing the server config
// package: NODE_STATS_ENV_FILE overrides the working-dir ./.env (orchestrators
// that relocate it into a persistent data volume set this).
func envFilePath() string {
	if p := strings.TrimSpace(os.Getenv("NODE_STATS_ENV_FILE")); p != "" {
		return p
	}
	return ".env"
}
