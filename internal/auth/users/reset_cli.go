package users

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"golang.org/x/term"
)

// RunResetPasswordCLI implements `node-stats reset-password <email> [password]`.
// It talks to the RUNNING server on this machine over loopback (the server must
// be up — the change has to go through Raft, which only the server can do).
// Password: 2nd argument, NODE_STATS_NEW_PASSWORD, or an interactive prompt.
// Port: ADDR from the environment / .env (NODE_STATS_ENV_FILE), default :8080;
// inside the docker image ADDR is :9090.
func RunResetPasswordCLI(args []string) {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(os.Stderr, "usage: node-stats reset-password <email> [new-password]")
		os.Exit(2)
	}
	email := strings.TrimSpace(args[0])
	password := ""
	if len(args) > 1 {
		password = args[1]
	} else if p := os.Getenv("NODE_STATS_NEW_PASSWORD"); p != "" {
		password = p
	} else {
		fmt.Fprintf(os.Stderr, "New password for %s: ", email)
		b, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "could not read password:", err)
			os.Exit(2)
		}
		password = string(b)
	}

	_ = godotenv.Load(envFile())
	addr := strings.TrimSpace(os.Getenv("ADDR"))
	if addr == "" {
		addr = ":8080"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		port = "8080"
	}
	body, _ := json.Marshal(LocalResetRequest{Email: email, NewPassword: password})
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+port+"/api/v1/auth/local-reset", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(LocalResetHeader, "1")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "node-stats is not reachable on 127.0.0.1:%s (%v) — it must be running; set ADDR if it listens elsewhere\n", port, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "reset failed (%s): %s\n", resp.Status, strings.TrimSpace(string(out)))
		os.Exit(1)
	}
	fmt.Printf("password for %s has been reset (replicated through the cluster)\n", email)
}

func envFile() string {
	if p := strings.TrimSpace(os.Getenv("NODE_STATS_ENV_FILE")); p != "" {
		return p
	}
	return ".env"
}
