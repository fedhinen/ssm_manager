package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Targets   []Target   `yaml:"targets,omitempty"`
	Bookmarks []Bookmark `yaml:"bookmarks,omitempty"`
}

type SessionType string

const (
	SessionTypeShell      SessionType = "shell"
	SessionTypeForward    SessionType = "port-forward"
	SessionTypeRemoteHost SessionType = "remote-host"
)

type Bookmark struct {
	Name         string      `yaml:"name"`
	Type         SessionType `yaml:"type"`
	Profile      string      `yaml:"profile"`
	Region       string      `yaml:"region"`
	InstanceID   string      `yaml:"instance_id"`
	InstanceName string      `yaml:"instance_name,omitempty"`
	Host         string      `yaml:"host,omitempty"`
	RemotePort   int         `yaml:"remote_port,omitempty"`
	LocalPort    int         `yaml:"local_port,omitempty"`
}

type Target struct {
	Name       string `yaml:"name"`
	Profile    string `yaml:"profile"`
	Region     string `yaml:"region"`
	Host       string `yaml:"host"`
	RemotePort int    `yaml:"remote_port"`
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determining user config directory: %w", err)
	}
	return filepath.Join(dir, "ssm-manager", "config.yaml"), nil
}

func Load(path string) (Config, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return emptyConfig(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("reading config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(contents, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config %q: %w", path, err)
	}
	if cfg.Targets == nil {
		cfg.Targets = []Target{}
	}
	if cfg.Bookmarks == nil {
		cfg.Bookmarks = []Bookmark{}
	}
	for _, target := range cfg.Targets {
		if err := validateTarget(target); err != nil {
			return Config{}, err
		}
	}
	for _, bookmark := range cfg.Bookmarks {
		if err := validateBookmark(bookmark); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	for _, target := range cfg.Targets {
		if err := validateTarget(target); err != nil {
			return err
		}
	}
	for _, bookmark := range cfg.Bookmarks {
		if err := validateBookmark(bookmark); err != nil {
			return err
		}
	}
	contents, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory %q: %w", dir, err)
	}
	temporary, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("creating temporary config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("securing temporary config: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return fmt.Errorf("writing temporary config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("closing temporary config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("saving config %q: %w", path, err)
	}
	return nil
}

func (c *Config) AddBookmark(bookmark Bookmark) error {
	if err := validateBookmark(bookmark); err != nil {
		return err
	}
	for _, existing := range c.Bookmarks {
		if existing.Name == bookmark.Name {
			return fmt.Errorf("bookmark %q already exists", bookmark.Name)
		}
	}
	c.Bookmarks = append(c.Bookmarks, bookmark)
	return nil
}

func (c Config) TargetsFor(profile, region string) []Target {
	targets := []Target{}
	for _, target := range c.Targets {
		profileMatches := target.Profile == "" || target.Profile == profile
		regionMatches := target.Region == "" || target.Region == region
		if profileMatches && regionMatches {
			targets = append(targets, target)
		}
	}
	return targets
}

func validateTarget(target Target) error {
	if target.Name == "" {
		return errors.New("config target has an empty name")
	}
	if target.Host == "" {
		return fmt.Errorf("config target %q has an empty host", target.Name)
	}
	if target.RemotePort < 1 || target.RemotePort > 65535 {
		return fmt.Errorf("config target %q has invalid remote_port %d", target.Name, target.RemotePort)
	}
	return nil
}

func validateBookmark(bookmark Bookmark) error {
	if bookmark.Name == "" {
		return errors.New("bookmark has an empty name")
	}
	if bookmark.Profile == "" || bookmark.Region == "" || bookmark.InstanceID == "" {
		return fmt.Errorf("bookmark %q requires profile, region, and instance_id", bookmark.Name)
	}
	switch bookmark.Type {
	case SessionTypeShell:
		return nil
	case SessionTypeForward:
		return validateBookmarkPorts(bookmark)
	case SessionTypeRemoteHost:
		if bookmark.Host == "" {
			return fmt.Errorf("bookmark %q requires host", bookmark.Name)
		}
		return validateBookmarkPorts(bookmark)
	default:
		return fmt.Errorf("bookmark %q has invalid type %q", bookmark.Name, bookmark.Type)
	}
}

func validateBookmarkPorts(bookmark Bookmark) error {
	remoteValid := bookmark.RemotePort >= 1 && bookmark.RemotePort <= 65535
	localValid := bookmark.LocalPort >= 1 && bookmark.LocalPort <= 65535
	if !remoteValid || !localValid {
		return fmt.Errorf("bookmark %q has invalid ports", bookmark.Name)
	}
	return nil
}

func emptyConfig() Config {
	return Config{
		Targets:   []Target{},
		Bookmarks: []Bookmark{},
	}
}
