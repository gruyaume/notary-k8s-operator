package main

import (
	"github.com/gruyaume/goops"
	"github.com/gruyaume/notary-k8s-operator/internal/charm"
)

func main() {
	env := goops.ReadEnv()

	if env.HookName == "" {
		goops.LogErrorf("No hook name found in environment")
		return
	}

	charm.HandleDefaultHook()
	charm.SetStatus()
}
