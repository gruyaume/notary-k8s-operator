package main

import (
	"github.com/gruyaume/goops"
	"github.com/gruyaume/notary-k8s-operator/internal/charm"
)

func main() {
	err := charm.Configure()
	if err != nil {
		goops.LogErrorf("Error handling default hook: %v", err)
		return
	}
}
