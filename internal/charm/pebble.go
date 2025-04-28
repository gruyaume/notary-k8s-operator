package charm

import (
	"bytes"
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

func pushFile(pebbleClient *client.Client, content string, path string) error {
	_, err := pebbleClient.SysInfo()
	if err != nil {
		return fmt.Errorf("could not connect to pebble: %w", err)
	}

	source := strings.NewReader(content)
	pushOptions := &client.PushOptions{
		Source: source,
		Path:   path,
	}

	err = pebbleClient.Push(pushOptions)
	if err != nil {
		return fmt.Errorf("could not push file: %w", err)
	}

	return nil
}

func getFileContent(pebbleClient *client.Client, path string) (string, error) {
	target := &bytes.Buffer{}
	opts := &client.PullOptions{
		Path:   path,
		Target: target,
	}

	err := pebbleClient.Pull(opts)
	if err != nil {
		return "", fmt.Errorf("could not get file content: %w", err)
	}

	return target.String(), nil
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

func startPebbleService(pebbleClient *client.Client) error {
	serviceOpts := &client.ServiceOptions{
		Names: []string{"notary"},
	}

	_, err := pebbleClient.Start(serviceOpts)
	if err != nil {
		return fmt.Errorf("could not start pebble service: %w", err)
	}

	return nil
}

func restartPebbleService(pebbleClient *client.Client) error {
	serviceOpts := &client.ServiceOptions{
		Names: []string{"notary"},
	}

	_, err := pebbleClient.Restart(serviceOpts)
	if err != nil {
		return fmt.Errorf("could not restart pebble service: %w", err)
	}

	return nil
}
