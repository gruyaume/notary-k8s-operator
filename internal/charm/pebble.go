package charm

import (
	"fmt"
	"strings"

	"github.com/canonical/pebble/client"
	"gopkg.in/yaml.v3"
)

const (
	socketPath = "/charm/containers/notary/pebble.socket"
)

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

func pushConfigFile(pebbleClient *client.Client, config []byte, path string) error {
	_, err := pebbleClient.SysInfo()
	if err != nil {
		return fmt.Errorf("could not connect to pebble: %w", err)
	}

	source := strings.NewReader(string(config))
	pushOptions := &client.PushOptions{
		Source: source,
		Path:   path,
	}

	err = pebbleClient.Push(pushOptions)
	if err != nil {
		return fmt.Errorf("could not push config file: %w", err)
	}

	return nil
}

func addPebbleLayer(pebbleClient *client.Client) error {
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

	addLayerOpts := &client.AddLayerOptions{
		Combine:   true,
		Label:     "notary",
		LayerData: layerData,
	}

	err = pebbleClient.AddLayer(addLayerOpts)
	if err != nil {
		return fmt.Errorf("could not add pebble layer: %w", err)
	}

	return nil
}
