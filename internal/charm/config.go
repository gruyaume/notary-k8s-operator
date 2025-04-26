package charm

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

const (
	DBPath = "/var/lib/notary/database/notary.db"
)

type SystemLoggingConfig struct {
	Level  string `yaml:"level"`
	Output string `yaml:"output"`
}
type LoggingConfig struct {
	System SystemLoggingConfig `yaml:"system"`
}

type NotaryConfig struct {
	KeyPath             string        `yaml:"key_path"`
	CertPath            string        `yaml:"cert_path"`
	DBPath              string        `yaml:"db_path"`
	Port                int           `yaml:"port"`
	PebbleNotifications bool          `yaml:"pebble_notifications"`
	Logging             LoggingConfig `yaml:"logging"`
}

func getExpectedConfig() ([]byte, error) {
	notaryConfig := NotaryConfig{
		KeyPath:             KeyPath,
		CertPath:            CertPath,
		DBPath:              DBPath,
		Port:                2111,
		PebbleNotifications: true,
		Logging: LoggingConfig{
			System: SystemLoggingConfig{
				Level:  "debug",
				Output: "stdout",
			},
		},
	}

	b, err := yaml.Marshal(notaryConfig)
	if err != nil {
		return nil, fmt.Errorf("could not marshal config to YAML: %w", err)
	}

	return b, nil
}
