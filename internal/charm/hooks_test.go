package charm_test

import (
	"testing"

	"github.com/gruyaume/goops"
	"github.com/gruyaume/goops/commands"
	"github.com/gruyaume/goops/environment"
	"github.com/gruyaume/notary-k8s-operator/internal/charm"
)

type MockCommandRunner struct{}

func (m *MockCommandRunner) Run(name string, args ...string) ([]byte, error) {
	return nil, nil
}

type MockEnvironmentGetter struct{}

func (m *MockEnvironmentGetter) Get(key string) string {
	return ""
}

func TestHandleDefaultHook(t *testing.T) {
	mR := &MockCommandRunner{}

	mG := &MockEnvironmentGetter{}

	hookContext := &goops.HookContext{
		Commands: &commands.Command{
			Runner: mR,
		},
		Environment: &environment.Environment{
			Getter: mG,
		},
	}
	charm.HandleDefaultHook(hookContext)
}
