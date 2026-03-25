package cluster_config

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/joho/godotenv"
)

// Store holds runtime cluster config. Updated on connect without restart.
var store struct {
	mu           sync.RWMutex
	mainNodeURL  string
	nodeToken    string
}

// Get returns current main URL and token. Safe for concurrent use.
func Get() (mainNodeURL, nodeToken string) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.mainNodeURL, store.nodeToken
}

// Update sets the config and persists to .env. Call after successful connect.
// In-memory store is always updated; file write failure is logged but does not fail the connect.
func Update(mainNodeURL, nodeAccessToken string) error {
	store.mu.Lock()
	store.mainNodeURL = mainNodeURL
	store.nodeToken = nodeAccessToken
	store.mu.Unlock()

	if err := WriteClusterConfig(mainNodeURL, nodeAccessToken); err != nil {
		// Log but don't fail - in-memory config is set, push will work for current session
		log.Printf("cluster_config: failed to persist .env (push will work until restart): %v", err)
	}
	return nil
}

// Clear removes the cluster agent config from the in-memory store and the .env file.
func Clear() error {
	store.mu.Lock()
	store.mainNodeURL = ""
	store.nodeToken = ""
	store.mu.Unlock()

	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	envPath := filepath.Join(wd, ".env")

	lines := make([]string, 0)
	if f, err := os.Open(envPath); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "MAIN_NODE_URL=") ||
				strings.HasPrefix(trimmed, "NODE_ACCESS_TOKEN=") ||
				trimmed == "# Cluster agent (push to main node)" {
				continue
			}
			lines = append(lines, line)
		}
		f.Close()
	}

	content := strings.Join(lines, "\n")
	if len(content) > 0 && content[len(content)-1] != '\n' {
		content += "\n"
	}
	return os.WriteFile(envPath, []byte(content), 0600)
}

// Load reads config from env (call at startup, after godotenv.Load).
func Load() {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.mainNodeURL = strings.TrimSuffix(os.Getenv("MAIN_NODE_URL"), "/")
	store.nodeToken = os.Getenv("NODE_ACCESS_TOKEN")
}

// LoadFromEnvFile loads .env then populates the store. Call at startup.
func LoadFromEnvFile() {
	wd, _ := os.Getwd()
	if wd == "" {
		wd = "."
	}
	_ = godotenv.Load(filepath.Join(wd, ".env"))
	Load()
}

// WriteClusterConfig appends or updates MAIN_NODE_URL and NODE_ACCESS_TOKEN in .env.
func WriteClusterConfig(mainNodeURL, nodeAccessToken string) error {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	envPath := filepath.Join(wd, ".env")

	// Read existing .env
	lines := make([]string, 0)
	if f, err := os.Open(envPath); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(strings.TrimSpace(line), "MAIN_NODE_URL=") ||
				strings.HasPrefix(strings.TrimSpace(line), "NODE_ACCESS_TOKEN=") {
				continue // Skip old values, we'll add new ones
			}
			lines = append(lines, line)
		}
		f.Close()
	}

	// Append cluster config
	lines = append(lines, "")
	lines = append(lines, "# Cluster agent (push to main node)")
	lines = append(lines, fmt.Sprintf("MAIN_NODE_URL=%s", escapeValue(mainNodeURL)))
	lines = append(lines, fmt.Sprintf("NODE_ACCESS_TOKEN=%s", escapeValue(nodeAccessToken)))

	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(envPath, []byte(content), 0600)
}

func escapeValue(value string) string {
	if strings.ContainsAny(value, " \t\n\"'$`\\") {
		escaped := strings.ReplaceAll(value, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		return fmt.Sprintf("\"%s\"", escaped)
	}
	return value
}
