package main

import (
	"context"
	"os"

	"github.com/gruyaume/goops"
	"github.com/gruyaume/goops/commands"
	tracingIntegration "github.com/gruyaume/notary-k8s/integrations/tracing"
	"github.com/gruyaume/notary-k8s/internal/charm"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func main() {
	hc := goops.NewHookContext()
	hook := hc.Environment.JujuHookName()

	if hook == "" {
		return
	}

	ti := tracingIntegration.Integration{
		HookContext:  hc,
		RelationName: "tracing",
		CharmName:    "notary-k8s",
	}

	ti.PublishSupportedProtocols([]tracingIntegration.Protocol{tracingIntegration.GRPC})
	endpoint := ti.GetEndpoint()

	ctx := context.Background()

	var tp *sdktrace.TracerProvider

	var err error

	if endpoint != "" {
		tp, err = tracingIntegration.InitTracer(ctx, tracingIntegration.TelemetryConfig{
			OTLPEndpoint:   endpoint,
			ServiceName:    "notary-k8s",
			ServiceVersion: "0.0.1", // pin to your build version
		})
		if err != nil {
			hc.Commands.JujuLog(commands.Error, "could not initialize tracer:", err.Error())
		} else {
			defer func() {
				err := tp.Shutdown(ctx)
				if err != nil {
					hc.Commands.JujuLog(commands.Error, "could not shutdown tracer:", err.Error())
				}
			}()
		}
	}

	tracer := otel.Tracer("notary-k8s")
	ctx, span := tracer.Start(ctx, hook)

	defer span.End()

	charm.HandleDefaultHook(ctx, hc)
	charm.SetStatus(ctx, hc)

	if tp != nil {
		tp.ForceFlush(ctx)
	}

	os.Exit(0)
}
