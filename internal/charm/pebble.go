package charm

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/canonical/pebble/client"
	"github.com/gruyaume/goops"
	"gopkg.in/yaml.v3"
)

func pushFile(pebbleClient goops.PebbleClient, content string, path string) error {
	_, err := pebbleClient.SysInfo()
	if err != nil {
		return fmt.Errorf("could not connect to pebble: %w", err)
	}

	source := strings.NewReader(content)

	err = pebbleClient.Push(&client.PushOptions{
		Source: source,
		Path:   path,
	})
	if err != nil {
		return fmt.Errorf("could not push file: %w", err)
	}

	return nil
}

func getFileContent(pebbleClient goops.PebbleClient, path string) (string, error) {
	target := &bytes.Buffer{}

	err := pebbleClient.Pull(&client.PullOptions{
		Path:   path,
		Target: target,
	})
	if err != nil {
		return "", fmt.Errorf("could not get file content: %w", err)
	}

	return target.String(), nil
}

type ServiceConfig struct {
	Override string `yaml:"override"`
	Summary  string `yaml:"summary"`
	Command  string `yaml:"command"`
	Startup  string `yaml:"startup"`
}

type PebbleLayer struct {
	Summary     string                   `yaml:"summary"`
	Description string                   `yaml:"description"`
	Services    map[string]ServiceConfig `yaml:"services"`
}

func addPebbleLayer(pebbleClient goops.PebbleClient) error {
	layerData, err := yaml.Marshal(PebbleLayer{
		Summary:     "Notary layer",
		Description: "pebble config layer for Notary",
		Services: map[string]ServiceConfig{
			"notary": {
				Override: "replace",
				Summary:  "Notary Service",
				Command:  "notary --config " + ConfigPath,
				Startup:  "enabled",
			},
		},
	})
	if err != nil {
		return fmt.Errorf("could not marshal layer data to YAML: %w", err)
	}

	err = pebbleClient.AddLayer(&client.AddLayerOptions{
		Combine:   true,
		Label:     "notary",
		LayerData: layerData,
	})
	if err != nil {
		return fmt.Errorf("could not add pebble layer: %w", err)
	}

	return nil
}
