package charm_test

import (
	"os"
	"testing"

	"github.com/gruyaume/goops"
	"github.com/gruyaume/goops/goopstest"
	"github.com/gruyaume/notary-k8s-operator/internal/charm"
	"gopkg.in/yaml.v3"
)

func TestGivenNotLeaderWhenConfigureThenBlocked(t *testing.T) {
	ctx := goopstest.Context{
		Charm: charm.Configure,
	}

	stateIn := &goopstest.State{
		Leader: false,
	}

	stateOut, err := ctx.Run("start", stateIn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stateOut.UnitStatus != string(goops.StatusBlocked) {
		t.Errorf("expected status %s, got %s", goops.StatusBlocked, stateOut.UnitStatus)
	}
}

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

func TestGivenLeaderWhenConfigureThenConfigFileIsPushed(t *testing.T) {
	ctx := goopstest.Context{
		Charm: charm.Configure,
	}

	dname, err := os.MkdirTemp("", "sampledir")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}

	defer os.RemoveAll(dname)

	stateIn := &goopstest.State{
		Leader: true,
		Containers: []*goopstest.Container{
			{
				Name:       "notary",
				CanConnect: true,
				Mounts: map[string]goopstest.Mount{
					"config": {
						Location: "/etc/notary/config",
						Source:   dname,
					},
				},
			},
		},
	}

	_, err = ctx.Run("install", stateIn)
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}

	content, err := os.ReadFile(dname + "/etc/notary/config/notary.yaml")
	if err != nil {
		t.Fatalf("Failed to read pushed file: %v", err)
	}

	expectedContentStruct := NotaryConfig{
		KeyPath:             charm.KeyPath,
		CertPath:            charm.CertPath,
		DBPath:              charm.DBPath,
		Port:                2111,
		PebbleNotifications: true,
		Logging: LoggingConfig{
			System: SystemLoggingConfig{
				Level:  "debug",
				Output: "stdout",
			},
		},
	}

	b, err := yaml.Marshal(expectedContentStruct)
	if err != nil {
		t.Fatalf("Failed to marshal expected content: %v", err)
	}

	if string(content) != string(b) {
		t.Errorf("Expected file content '%s', got '%s'", string(b), string(content))
	}
}
