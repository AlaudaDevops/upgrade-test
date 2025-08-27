// pkg/config/config.go

package config

import (
	"os"

	"gopkg.in/yaml.v2"
)

const (
	defaultOperatorNamespace = "testing-upgrade-namespace"
	defaultSystemNamespace   = "cpaas-system"
)

// Config represents the main configuration structure
type Config struct {
	UpgradePaths []UpgradePath `yaml:"upgradePaths,omitempty"`
	Immediate    bool          `yaml:"immediate,omitempty"`
	Workspace    string        `yaml:"workspace,omitempty"`
	LogLevel     string        `yaml:"logLevel,omitempty"`
	Artifact     string        `yaml:"artifact,omitempty"`

	PrepareCommand    string `yaml:"prepareCommand,omitempty"`
	OperatorNamespace string `yaml:"operatorNamespace,omitempty"`
	OperatorName      string `yaml:"operatorName,omitempty"`
}

// Operator represents the operator configuration
type Operator struct {
	OperatorNamespace string `yaml:"operatorNamespace,omitempty"`
	SystemNamespace   string `yaml:"systemNamespace,omitempty"`
	Channel           string `yaml:"channel,omitempty"`
	Cleanup           bool   `yaml:"cleanup,omitempty"`
	MaxRetries        int    `yaml:"maxRetries,omitempty"`
	ArtifactName      string `yaml:"artifactName,omitempty"`
}

// UpgradePath represents a single upgrade path
type UpgradePath struct {
	Name     string    `yaml:"name,omitempty"`
	Versions []Version `yaml:"versions,omitempty"`
}

// Version represents a single version in the upgrade path
type Version struct {
	Name          string `yaml:"name,omitempty"`
	BundleVersion string `yaml:"bundleVersion,omitempty"`
	TestCommand   string `yaml:"testCommand,omitempty"`
	TestSubPath   string `yaml:"testSubPath,omitempty"`
	BuildCommand  string `yaml:"buildCommand,omitempty"`
	Revision      string `yaml:"revision,omitempty"`
}

// LoadConfig loads the configuration from a YAML file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return defaultConfig(&config), nil
}

func defaultConfig(config *Config) *Config {
	if config.Workspace == "" {
		config.Workspace = "./"
	}

	// if config.Operator.MaxRetries == 0 {
	// 	config.Operator.MaxRetries = 60
	// }

	// if config.Operator.OperatorNamespace == "" {
	// 	config.Operator.OperatorNamespace = defaultOperatorNamespace
	// }

	// if config.Operator.SystemNamespace == "" {
	// 	config.Operator.SystemNamespace = defaultSystemNamespace
	// }

	return config
}
