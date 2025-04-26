package charm

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

func getExpectedConfig() ([]byte, error) {
	notaryConfig := NotaryConfig{
		KeyPath:             KeyPath,
		CertPath:            CertPath,
		DBPath:              DBPath,
		Port:                2111,
		PebbleNotifications: true,
		Logging: LoggingConfig{
			Level:  "debug",
			Output: "stdout",
		},
	}
	b, err := yaml.Marshal(notaryConfig)
	if err != nil {
		return nil, fmt.Errorf("could not marshal config to YAML: %w", err)
	}

	return b, nil
}
