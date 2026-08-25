// Package aws contains AWS inventory and discovery concerns. It deliberately
// knows nothing about Bubble Tea or how SSM child processes are presented.
package aws

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/victorruiz/ssm-manager/internal/awscli"
)

type Instance = awscli.Instance
type RemoteResource = awscli.RemoteHost
type DiscoveryResult = awscli.DiscoveryResult

type Profile struct {
	Name   string `json:"name"`
	Region string `json:"region,omitempty"`
}

type Inventory interface {
	Profiles(context.Context) ([]Profile, error)
	Regions(context.Context, string) ([]string, error)
	Instances(context.Context, string, string) ([]Instance, error)
	DiscoverRemoteResources(context.Context, string, string, string) DiscoveryResult
}

// Service adapts the AWS CLI transport to a presentation-independent API.
type Service struct {
	client     *awscli.Client
	configPath string
}

func NewService(client *awscli.Client, configPath string) *Service {
	return &Service{client: client, configPath: configPath}
}

func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".aws", "config")
}

func (s *Service) Profiles(ctx context.Context) ([]Profile, error) {
	profiles, err := ParseProfiles(s.configPath)
	if err == nil && len(profiles) > 0 {
		return profiles, nil
	}

	// The CLI fallback supports credentials-only profiles and unusual AWS config
	// providers while the normal path remains the requested ~/.aws/config parser.
	names, cliErr := s.client.Profiles(ctx)
	if cliErr != nil {
		if err != nil {
			return nil, errors.Join(err, cliErr)
		}
		return nil, cliErr
	}
	profiles = make([]Profile, 0, len(names))
	for _, name := range names {
		profiles = append(profiles, Profile{Name: name})
	}
	return profiles, nil
}

func (s *Service) Regions(ctx context.Context, profile string) ([]string, error) {
	return s.client.Regions(ctx, profile)
}

func (s *Service) Instances(ctx context.Context, profile, region string) ([]Instance, error) {
	return s.client.Instances(ctx, profile, region)
}

func (s *Service) DiscoverRemoteResources(
	ctx context.Context,
	profile string,
	region string,
	vpcID string,
) DiscoveryResult {
	return s.client.DiscoverRemoteHosts(ctx, profile, region, vpcID)
}

func ParseProfiles(path string) ([]Profile, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening AWS config %q: %w", path, err)
	}
	defer file.Close()

	profiles := []Profile{}
	index := map[string]int{}
	current := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			current = ""
			switch {
			case section == "default":
				current = section
			case strings.HasPrefix(section, "profile "):
				current = strings.TrimSpace(strings.TrimPrefix(section, "profile "))
			}
			if current == "" {
				continue
			}
			if _, exists := index[current]; !exists {
				index[current] = len(profiles)
				profiles = append(profiles, Profile{Name: current})
			}
			continue
		}
		if current == "" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if found && strings.EqualFold(strings.TrimSpace(key), "region") {
			profiles[index[current]].Region = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading AWS config %q: %w", path, err)
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		if profiles[i].Name == profiles[j].Name {
			return false
		}
		if profiles[i].Name == "default" {
			return true
		}
		if profiles[j].Name == "default" {
			return false
		}
		return profiles[i].Name < profiles[j].Name
	})
	return profiles, nil
}
