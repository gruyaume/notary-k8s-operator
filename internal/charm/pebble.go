package charm

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/canonical/pebble/client"
	"gopkg.in/yaml.v3"
)

func pushFile(pebbleClient *client.Client, content string, path string) error {
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

func getFileContent(pebbleClient *client.Client, path string) (string, error) {
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

type Check struct {
	Override  string `yaml:"override"`
	Level     string `yaml:"level"`
	Startup   string `yaml:"startup"`
	Period    string `yaml:"period"`
	Timeout   string `yaml:"timeout"`
	Threshold string `yaml:"threshold"`
	HTTP      string `yaml:"http"`
	TCP       string `yaml:"tcp"`
	Exec      string `yaml:"exec"`
}

type LogTarget struct {
	Override string            `yaml:"override"`
	Type     string            `yaml:"type"`
	Location string            `yaml:"location"`
	Services []string          `yaml:"services"`
	Labels   map[string]string `yaml:"labels"`
}

type PebblePlan struct {
	Services   map[string]ServiceConfig `yaml:"services"`
	Checks     map[string]Check         `yaml:"checks"`
	LogTargets map[string]LogTarget     `yaml:"log-targets"`
}

func pebbleLayerCreated(pebbleClient *client.Client) bool {
	_, err := pebbleClient.SysInfo()
	if err != nil {
		return false
	}

	dataBytes, err := pebbleClient.PlanBytes(nil)
	if err != nil {
		return false
	}

	var plan PebblePlan

	err = yaml.Unmarshal(dataBytes, &plan)
	if err != nil {
		return false
	}

	service, exists := plan.Services["notary"]
	if !exists {
		return false
	}

	if service.Command != "notary --config "+ConfigPath {
		return false
	}

	return true
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
