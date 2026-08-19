package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Targets []Target `yaml:"targets"`
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
		return Config{Targets: []Target{}}, nil
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
	for _, target := range cfg.Targets {
		if err := validateTarget(target); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
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
