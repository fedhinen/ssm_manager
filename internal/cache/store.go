// Package cache provides the small JSON cache used by both TUI and future CLI modes.
package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Store struct {
	Dir string
	TTL time.Duration
}

func New(appName string, ttl time.Duration) (Store, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return Store{}, fmt.Errorf("determining cache directory: %w", err)
	}
	return Store{Dir: filepath.Join(dir, appName), TTL: ttl}, nil
}

func (s Store) Load(key string, destination any) (bool, error) {
	path := s.path(key)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking cache %q: %w", path, err)
	}
	if s.TTL > 0 && time.Since(info.ModTime()) > s.TTL {
		return false, nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("reading cache %q: %w", path, err)
	}
	if err := json.Unmarshal(contents, destination); err != nil {
		return false, fmt.Errorf("decoding cache %q: %w", path, err)
	}
	return true, nil
}

func (s Store) Save(key string, value any) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("creating cache directory: %w", err)
	}
	contents, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encoding cache: %w", err)
	}
	path := s.path(key)
	temporary, err := os.CreateTemp(s.Dir, ".cache-*.json")
	if err != nil {
		return fmt.Errorf("creating cache file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("saving cache %q: %w", path, err)
	}
	return nil
}

func (s Store) path(key string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", "..", "-")
	return filepath.Join(s.Dir, replacer.Replace(key)+".json")
}
