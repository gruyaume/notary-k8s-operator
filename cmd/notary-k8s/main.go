package main

import (
	"github.com/gruyaume/goops"
	"github.com/gruyaume/goops/commands"
	"github.com/gruyaume/notary-k8s-operator/internal/charm"
)

func main() {
	hc := goops.NewHookContext()
	hook := hc.Environment.JujuHookName()

	if hook == "" {
		hc.Commands.JujuLog(commands.Error, "No hook name found in environment")
		return
	}

	charm.HandleDefaultHook(hc)
	charm.SetStatus(hc)
}
