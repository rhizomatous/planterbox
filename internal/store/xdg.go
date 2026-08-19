package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// appDir is plbx's directory name under whichever base the platform gives us.
const appDir = "planterbox"

// Env supplies the environment a root is resolved against. Injecting it keeps
// the resolution testable without touching the real environment.
type Env struct {
	GOOS   string
	Home   string
	Getenv func(string) string
}

// HostEnv returns the running host's environment.
func HostEnv(goos string) (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	return Env{GOOS: goos, Home: home, Getenv: os.Getenv}, nil
}

// Root resolves plbx's state directory: everything it keeps on disk lives
// under this one path, records in a subdirectory of it.
//
// PLBX_STATE_DIR wins outright. Otherwise XDG_DATA_HOME is honored on every
// platform (Linux by convention, macOS because anyone who sets it means it),
// falling back to ~/Library/Application Support on macOS and ~/.local/share
// elsewhere.
func Root(env Env) (string, error) {
	if dir := env.Getenv("PLBX_STATE_DIR"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", fmt.Errorf("PLBX_STATE_DIR must be an absolute path, got %q", dir)
		}
		return dir, nil
	}
	if dir := env.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, appDir), nil
	}
	if env.Home == "" {
		return "", errors.New("cannot resolve a state directory: no home directory")
	}
	base := filepath.Join(env.Home, ".local", "share")
	if env.GOOS == "darwin" {
		base = filepath.Join(env.Home, "Library", "Application Support")
	}
	return filepath.Join(base, appDir), nil
}
