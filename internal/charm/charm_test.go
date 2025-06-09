package charm_test

import (
	"testing"

	"github.com/gruyaume/goops"
	"github.com/gruyaume/goops/goopstest"
	"github.com/gruyaume/notary-k8s-operator/internal/charm"
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
