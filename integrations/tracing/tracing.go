package tracing

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gruyaume/goops"
	"github.com/gruyaume/goops/commands"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

type Integration struct {
	HookContext  *goops.HookContext
	RelationName string
	CharmName    string
}

type Protocol string

const (
	GRPC Protocol = "otlp_grpc"
	HTTP Protocol = "otlp_http"
)

type ReceiverProtocol struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type ProviderReceivers struct {
	Protocol ReceiverProtocol `json:"protocol"`
	Url      string           `json:"url"`
}

func (i *Integration) GetRelationID() (string, error) {
	relationIDs, err := i.HookContext.Commands.RelationIDs(&commands.RelationIDsOptions{
		Name: i.RelationName,
	})
	if err != nil {
		return "", fmt.Errorf("could not get relation IDs: %w", err)
	}

	if len(relationIDs) == 0 {
		return "", fmt.Errorf("no relation IDs found for %s", i.RelationName)
	}

	return relationIDs[0], nil
}

func (i *Integration) PublishSupportedProtocols(protocols []Protocol) {
	relationID, err := i.GetRelationID()
	if err != nil {
		i.HookContext.Commands.JujuLog(commands.Error, "Could not get relation ID:", err.Error())
		return
	}

	var supportedProtocols []string
	for _, protocol := range protocols {
		supportedProtocols = append(supportedProtocols, string(protocol))
	}

	receiversBytes, err := json.Marshal(supportedProtocols)
	if err != nil {
		i.HookContext.Commands.JujuLog(commands.Error, "Could not marshal supported protocols to JSON:", err.Error())
		return
	}

	relationData := map[string]string{
		"receivers": string(receiversBytes),
	}

	err = i.HookContext.Commands.RelationSet(&commands.RelationSetOptions{
		ID:   relationID,
		App:  true,
		Data: relationData,
	})
	if err != nil {
		i.HookContext.Commands.JujuLog(commands.Error, "Could not set relation data:", err.Error())
		return
	}
}

func (i *Integration) GetEndpoint() string {
	relationID, err := i.GetRelationID()
	if err != nil {
		i.HookContext.Commands.JujuLog(commands.Error, "Could not get relation ID:", err.Error())
		return ""
	}

	relations, err := i.HookContext.Commands.RelationList(&commands.RelationListOptions{
		ID: relationID,
	})
	if err != nil {
		i.HookContext.Commands.JujuLog(commands.Error, "Could not get relation list:", err.Error())
		return ""
	}

	if len(relations) == 0 {
		i.HookContext.Commands.JujuLog(commands.Error, "No relations found for ID:", relationID)
		return ""
	}

	relationData, err := i.HookContext.Commands.RelationGet(&commands.RelationGetOptions{
		ID:     relationID,
		UnitID: relations[0],
		App:    true,
	})
	if err != nil {
		i.HookContext.Commands.JujuLog(commands.Error, "Could not get relation data:", err.Error())
		return ""
	}

	receiversStr := relationData["receivers"]
	if receiversStr == "" {
		i.HookContext.Commands.JujuLog(commands.Error, "No receivers found in relation data")
		return ""
	}

	var providerReceivers []*ProviderReceivers

	err = json.Unmarshal([]byte(receiversStr), &providerReceivers)
	if err != nil {
		i.HookContext.Commands.JujuLog(commands.Error, "Could not unmarshal receivers:", err.Error())
		return ""
	}

	if len(providerReceivers) == 0 {
		i.HookContext.Commands.JujuLog(commands.Error, "No provider receivers found")
		return ""
	}

	for _, receiver := range providerReceivers {
		if receiver.Protocol.Name == string(GRPC) {
			return receiver.Url
		}
	}

	i.HookContext.Commands.JujuLog(commands.Error, "No gRPC receiver found")

	return ""
}

type TelemetryConfig struct {
	Enabled        bool
	OTLPEndpoint   string
	ServiceName    string
	ServiceVersion string
}

// InitTracer sets up a global TracerProvider based on the given configuration.
func InitTracer(ctx context.Context, cfg TelemetryConfig) (*sdktrace.TracerProvider, error) {
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	sampler := sdktrace.AlwaysSample()

	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
			attribute.String("service.version", cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sampler),
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp, nil
}
