package main

import (
	"os"

	"github.com/gruyaume/goops"
	"github.com/gruyaume/notary-k8s/internal/charm"
)

func main() {
	hookContext := goops.NewHookContext()
	hookName := hookContext.Environment.JujuHookName()
	if hookName != "" {
		charm.HandleDefaultHook(hookContext)
		charm.SetStatus(hookContext)
		os.Exit(0)
	}
}
