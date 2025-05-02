package main

import (
	"context"

	"github.com/gruyaume/goops"
	"github.com/gruyaume/goops/commands"
	tracingIntegration "github.com/gruyaume/notary-k8s/integrations/tracing"
	"github.com/gruyaume/notary-k8s/internal/charm"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	serviceName    = "notary-k8s"
	serviceVersion = "0.0.1" // pin to your build version
	relationName   = "tracing"
)

func main() {
	hc := goops.NewHookContext()
	hook := hc.Environment.JujuHookName()

	if hook == "" {
		return
	}

	run(hc, hook)
}

// run initializes tracing, starts the root span, dispatches hooks, and ensures shutdown.
func run(hc *goops.HookContext, hook string) {
	ctx, tp := initTracing(hc)
	// ensure tracer is shut down
	defer shutdown(tp, ctx)

	tracer := otel.Tracer(serviceName)
	ctx, span := tracer.Start(ctx, hook)

	defer span.End()

	// execute charm hooks under span
	charm.HandleDefaultHook(ctx, hc)
	charm.SetStatus(ctx, hc)

	flush(tp, ctx)
}

// initTracing sets up the tracing integration and returns ctx and TracerProvider (or nil).
func initTracing(hc *goops.HookContext) (context.Context, *sdktrace.TracerProvider) {
	ti := tracingIntegration.Integration{
		HookContext:  hc,
		RelationName: relationName,
		CharmName:    serviceName,
		ServiceName:  serviceName,
	}
	ti.PublishSupportedProtocols([]tracingIntegration.Protocol{tracingIntegration.GRPC})

	ctx := context.Background()

	tp, err := ti.InitTracer(ctx)
	if err != nil {
		hc.Commands.JujuLog(commands.Error, "could not initialize tracer:", err.Error())
		return ctx, nil
	}

	return ctx, tp
}

// flush ensures all spans are exported before shutdown.
func flush(tp *sdktrace.TracerProvider, ctx context.Context) {
	if tp != nil {
		tp.ForceFlush(ctx)
	}
}

// shutdown cleanly stops the tracer provider.
func shutdown(tp *sdktrace.TracerProvider, ctx context.Context) {
	if tp == nil {
		return
	}

	if err := tp.Shutdown(ctx); err != nil {
		hc := goops.NewHookContext()
		hc.Commands.JujuLog(commands.Error, "could not shutdown tracer:", err.Error())
	}
}
