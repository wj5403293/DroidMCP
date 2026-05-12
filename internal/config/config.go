// Package config handles environment-based configuration using Viper.
// DroidMCP follows a 12-factor app approach for configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Env prefix and defaults are kept as package constants so tests and callers
// can refer to the same values without restating them.
const (
	envPrefix = "DROIDMCP"

	defaultPort = 3000
	defaultRoot = "/"

	minPort = 1
	maxPort = 65535
)

// Config holds the application-wide configuration parameters. The validated
// Port and Root remain public for backwards compatibility with existing
// call sites; per-tool servers that need to read additional environment
// variables should use the Get* helpers (which delegate to viper) so keys
// stay 12-factor and do not require schema changes here.
type Config struct {
	Port int    // HTTP port for the MCP server
	Root string // Root directory for filesystem operations

	v *viper.Viper
}

// LoadConfig initializes a fresh Viper instance, loads configuration from
// environment variables and validates Port and Root. All variables are
// prefixed with DROIDMCP_ (e.g., DROIDMCP_PORT). Using viper.New() instead
// of the package-global keeps state isolated per process and per test.
func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetDefault("PORT", defaultPort)
	v.SetDefault("ROOT", defaultRoot)

	v.SetEnvPrefix(envPrefix)
	// Replace dots with underscores in env keys to support nested structs if needed.
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	cfg := &Config{
		Port: v.GetInt("PORT"),
		Root: v.GetString("ROOT"),
		v:    v,
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate enforces invariants the rest of the code depends on. It runs
// once at LoadConfig time so per-server main() functions can fail-fast
// with a clear message instead of crashing later on a bad port or a
// missing root directory.
func (c *Config) validate() error {
	if c.Port < minPort || c.Port > maxPort {
		return fmt.Errorf("DROIDMCP_PORT out of range: got %d, want [%d, %d]", c.Port, minPort, maxPort)
	}
	if c.Root == "" {
		return errors.New("DROIDMCP_ROOT must not be empty")
	}
	info, err := os.Stat(c.Root)
	if err != nil {
		return fmt.Errorf("DROIDMCP_ROOT %q: %w", c.Root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("DROIDMCP_ROOT %q: not a directory", c.Root)
	}
	return nil
}

// Viper exposes the underlying viper instance for callers that need
// behavior the typed helpers below do not cover (Unmarshal, ReadInConfig,
// etc.). Most callers should prefer the Get* helpers.
func (c *Config) Viper() *viper.Viper { return c.v }

// GetString reads a DROIDMCP_<KEY> string. Returns "" when unset.
func (c *Config) GetString(key string) string { return c.v.GetString(key) }

// GetInt reads a DROIDMCP_<KEY> integer. Returns 0 when unset or
// non-numeric (viper's behavior).
func (c *Config) GetInt(key string) int { return c.v.GetInt(key) }

// GetBool reads a DROIDMCP_<KEY> bool (1/0, true/false, etc.).
func (c *Config) GetBool(key string) bool { return c.v.GetBool(key) }

// GetDuration reads a DROIDMCP_<KEY> as a Go duration string (e.g. "30s",
// "5m"). Returns 0 when unset.
func (c *Config) GetDuration(key string) time.Duration { return c.v.GetDuration(key) }

// GetStringSlice reads a DROIDMCP_<KEY> as a comma-separated list. Viper's
// own GetStringSlice does not split env-sourced values, so we do the split
// here to keep the 12-factor convention predictable. Empty entries are
// dropped and surrounding whitespace is trimmed.
func (c *Config) GetStringSlice(key string) []string {
	raw := c.v.GetString(key)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// IsSet reports whether DROIDMCP_<KEY> was explicitly provided in the env
// (i.e. not just the default). Useful when "absent" and "empty string"
// must be distinguished.
func (c *Config) IsSet(key string) bool { return c.v.IsSet(key) }

// ResolveAPIKey returns the API key the named server should enforce on inbound
// requests. It checks the per-server variable DROIDMCP_<NAME>_KEY first, then
// falls back to the global DROIDMCP_API_KEY. An empty result means no auth is
// configured (dev mode); callers that require a key must enforce that themselves.
func ResolveAPIKey(serverName string) string {
	specific := envPrefix + "_" + strings.ToUpper(serverName) + "_KEY"
	if k := os.Getenv(specific); k != "" {
		return k
	}
	return os.Getenv(envPrefix + "_API_KEY")
}
