package charm

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strings"

	"github.com/canonical/pebble/client"
	"github.com/gruyaume/goops"
	"github.com/gruyaume/goops/commands"
	"github.com/gruyaume/goops/metadata"
	"github.com/gruyaume/notary-k8s/integrations/certificates"
	"github.com/gruyaume/notary-k8s/integrations/prometheus"
	"github.com/gruyaume/notary-k8s/internal/notary"
	"go.opentelemetry.io/otel"
)

const (
	KeyPath                = "/etc/notary/config/key.pem"
	CertPath               = "/etc/notary/config/cert.pem"
	ConfigPath             = "/etc/notary/config/notary.yaml"
	APIPort                = 2111
	CharmAccountUsername   = "charm@notary.com"
	NotaryLoginSecretLabel = "NOTARY_LOGIN"
	MetricsIntegrationName = "metrics"
	TLSIntegrationName     = "certificates"
)

// HandleDefaultHook handles charm events. It is the main entry point for the charm.
func HandleDefaultHook(ctx context.Context, hookContext *goops.HookContext) {
	ctx, span := otel.Tracer("notary-k8s").Start(ctx, "HandleDefaultHook")
	defer span.End()

	err := ensureLeader(ctx, hookContext)
	if err != nil {
		return
	}

	metadata, err := metadata.GetCharmMetadata(hookContext.Environment)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not get charm metadata:", err.Error())
		return
	}

	writePrometheus(ctx, hookContext, metadata.Name)

	err = setPorts(ctx, hookContext)
	if err != nil {
		return
	}

	pebble, err := client.New(&client.Config{Socket: socketPath})
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not connect to pebble:", err.Error())
		return
	}

	err = syncConfig(ctx, hookContext, pebble)
	if err != nil {
		return
	}

	changed, err := syncCertificate(ctx, hookContext, pebble)
	if err != nil {
		return
	}

	err = syncPebbleService(ctx, hookContext, pebble, changed)
	if err != nil {
		return
	}

	err = createAdminAccount(ctx, hookContext, pebble)
	if err != nil {
		return
	}

	hookContext.Commands.JujuLog(commands.Info, "Admin account created")
}

func setPorts(ctx context.Context, hookContext *goops.HookContext) error {
	_, span := otel.Tracer("notary-k8s").Start(ctx, "setPorts")
	defer span.End()

	setPortOpts := &commands.SetPortsOptions{
		Ports: []*commands.Port{
			{
				Port:     APIPort,
				Protocol: "tcp",
			},
		},
	}

	err := hookContext.Commands.SetPorts(setPortOpts)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not set ports:", err.Error())
		return fmt.Errorf("could not set ports: %w", err)
	}

	hookContext.Commands.JujuLog(commands.Info, "Ports set")

	return nil
}

func ensureLeader(ctx context.Context, hookContext *goops.HookContext) error {
	_, span := otel.Tracer("notary-k8s").Start(ctx, "ensureLeader")
	defer span.End()

	isLeader, err := hookContext.Commands.IsLeader()
	if err != nil {
		hookContext.Commands.JujuLog(commands.Warning, "Could not check if unit is leader:", err.Error())
		return fmt.Errorf("could not check if unit is leader: %w", err)
	}

	if !isLeader {
		hookContext.Commands.JujuLog(commands.Warning, "Unit is not leader")
		return fmt.Errorf("unit is not leader")
	}

	hookContext.Commands.JujuLog(commands.Info, "Unit is leader")

	return nil
}

func writePrometheus(ctx context.Context, hookContext *goops.HookContext, charmName string) {
	_, span := otel.Tracer("notary-k8s").Start(ctx, "writePrometheus")
	defer span.End()

	prometheusIntegration := &prometheus.Integration{
		HookContext:  hookContext,
		RelationName: MetricsIntegrationName,
		CharmName:    charmName,
		Jobs: []*prometheus.Job{
			{
				Scheme:      "https",
				TLSConfig:   prometheus.TLSConfig{InsecureSkipVerify: true},
				MetricsPath: "/metrics",
				StaticConfigs: []prometheus.StaticConfig{
					{
						Targets: []string{getHostname(hookContext)},
					},
				},
			},
		},
	}

	err := prometheusIntegration.Write()
	if err != nil {
		hookContext.Commands.JujuLog(commands.Debug, "Could not write prometheus integration:", err.Error())
		return
	}

	hookContext.Commands.JujuLog(commands.Info, "Prometheus integration written")
}

func syncConfig(ctx context.Context, hookContext *goops.HookContext, pebble *client.Client) error {
	_, span := otel.Tracer("notary-k8s").Start(ctx, "syncConfig")
	defer span.End()

	expectedConfig, err := getExpectedConfig()
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not get expected config:", err.Error())
		return fmt.Errorf("could not get expected config: %w", err)
	}

	err = pushFile(pebble, string(expectedConfig), "/etc/notary/config/notary.yaml")
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not push config file:", err.Error())
		return fmt.Errorf("could not push config file: %w", err)
	}

	hookContext.Commands.JujuLog(commands.Info, "Config file pushed")

	return nil
}

func syncPebbleService(ctx context.Context, hookContext *goops.HookContext, pebble *client.Client, restart bool) error {
	_, span := otel.Tracer("notary-k8s").Start(ctx, "syncPebbleService")
	defer span.End()

	err := addPebbleLayer(pebble)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not add pebble layer:", err.Error())
		return fmt.Errorf("could not add pebble layer: %w", err)
	}

	if restart {
		err := restartPebbleService(pebble)
		if err != nil {
			hookContext.Commands.JujuLog(commands.Error, "Could not restart pebble service:", err.Error())
			return fmt.Errorf("could not restart pebble service: %w", err)
		}

		hookContext.Commands.JujuLog(commands.Info, "Pebble service restarted")
	}

	hookContext.Commands.JujuLog(commands.Info, "Pebble layer added")

	err = startPebbleService(pebble)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not start pebble service:", err.Error())
		return fmt.Errorf("could not start pebble service: %w", err)
	}

	hookContext.Commands.JujuLog(commands.Info, "Pebble service started")

	return nil
}

func tlsIntegrationCreated(hookContext *goops.HookContext) bool {
	relationIDs, err := hookContext.Commands.RelationIDs(&commands.RelationIDsOptions{
		Name: TLSIntegrationName,
	})
	if err != nil {
		return false
	}

	if len(relationIDs) == 0 {
		return false
	}

	return true
}

func syncSelfSignedCertificate(hookContext *goops.HookContext, pebble *client.Client) (bool, error) {
	certContent, _ := getFileContent(pebble, CertPath)

	if certContent != "" {
		hookContext.Commands.JujuLog(commands.Info, "Certificate already exists, skipping generation")
		return false, nil
	}

	cert, key, err := generateCertificate()
	if err != nil {
		return false, fmt.Errorf("could not generate certificate: %w", err)
	}

	hookContext.Commands.JujuLog(commands.Info, "Certificate generated")

	err = pushFile(pebble, cert, "/etc/notary/config/cert.pem")
	if err != nil {
		return false, fmt.Errorf("could not push certificate: %w", err)
	}

	hookContext.Commands.JujuLog(commands.Info, "Certificate pushed")

	err = pushFile(pebble, key, "/etc/notary/config/key.pem")
	if err != nil {
		return false, fmt.Errorf("could not push key: %w", err)
	}

	hookContext.Commands.JujuLog(commands.Info, "Key pushed")

	return true, nil
}

func syncTlsProviderCertificate(hookContext *goops.HookContext, pebble *client.Client) (bool, error) {
	changed := false
	tlsIntegration := certificates.Integration{
		HookContext:  hookContext,
		RelationName: TLSIntegrationName,
		CertificateRequest: certificates.CertificateRequestAttributes{
			CommonName:          getHostname(hookContext),
			SansDNS:             []string{getHostname(hookContext)},
			SansIP:              []string{"127.0.0.1"},
			CountryName:         "CA",
			StateOrProvinceName: "QC",
			LocalityName:        "Montreal",
		},
	}

	err := tlsIntegration.Request()
	if err != nil {
		return changed, fmt.Errorf("could not request certificate: %w", err)
	}

	hookContext.Commands.JujuLog(commands.Info, "Certificate requested")

	providerCert, err := tlsIntegration.GetProviderCertificate()
	if err != nil {
		return changed, fmt.Errorf("could not get certificate: %w", err)
	}

	if len(providerCert) == 0 {
		return changed, fmt.Errorf("no certificate found")
	}

	if providerCert[0].Certificate == "" {
		return changed, fmt.Errorf("certificate is empty")
	}

	hookContext.Commands.JujuLog(commands.Info, "Certificate received")

	privateKey, err := tlsIntegration.GetPrivateKey()
	if err != nil {
		return changed, fmt.Errorf("could not get private key: %w", err)
	}

	existingPrivateKey, _ := getFileContent(pebble, KeyPath)

	if existingPrivateKey != privateKey {
		hookContext.Commands.JujuLog(commands.Warning, "Private key is different")

		err = pushFile(pebble, privateKey, KeyPath)
		if err != nil {
			return changed, fmt.Errorf("could not push key: %w", err)
		}

		hookContext.Commands.JujuLog(commands.Info, "Key pushed")

		changed = true
	}

	existingCertificate, _ := getFileContent(pebble, CertPath)
	if existingCertificate != providerCert[0].Certificate {
		hookContext.Commands.JujuLog(commands.Warning, "Certificate is different")

		err = pushFile(pebble, providerCert[0].Certificate, CertPath)
		if err != nil {
			return changed, fmt.Errorf("could not push certificate: %w", err)
		}

		hookContext.Commands.JujuLog(commands.Info, "Certificate pushed")

		changed = true
	}

	return changed, nil
}

func syncCertificate(ctx context.Context, hookContext *goops.HookContext, pebble *client.Client) (bool, error) {
	_, span := otel.Tracer("notary-k8s").Start(ctx, "syncCertificate")
	defer span.End()

	var changed bool

	var err error

	if !tlsIntegrationCreated(hookContext) {
		hookContext.Commands.JujuLog(commands.Info, "TLS integration not created")

		changed, err = syncSelfSignedCertificate(hookContext, pebble)
		if err != nil {
			hookContext.Commands.JujuLog(commands.Error, "Could not sync self signed certificate:", err.Error())
			return false, fmt.Errorf("could not sync self signed certificate: %v", err)
		}
	} else {
		changed, err = syncTlsProviderCertificate(hookContext, pebble)
		if err != nil {
			hookContext.Commands.JujuLog(commands.Error, "Could not sync tls provider certificate:", err.Error())
			return false, fmt.Errorf("could not sync tls provider certificate: %v", err)
		}
	}

	hookContext.Commands.JujuLog(commands.Info, "Synced TLS certificate")

	return changed, nil
}

func SetStatus(ctx context.Context, hookContext *goops.HookContext) {
	_, span := otel.Tracer("notary-k8s").Start(ctx, "SetStatus")
	defer span.End()

	status := commands.StatusActive

	message := ""

	statusSetOpts := &commands.StatusSetOptions{
		Name:    status,
		Message: message,
	}

	err := hookContext.Commands.StatusSet(statusSetOpts)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not set status:", err.Error())
		return
	}

	hookContext.Commands.JujuLog(commands.Info, "Status set to active")
}

func createAdminAccount(ctx context.Context, hookContext *goops.HookContext, pebble *client.Client) error {
	_, span := otel.Tracer("notary-k8s").Start(ctx, "createAdminAccount")
	defer span.End()

	cert, err := getFileContent(pebble, CertPath)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Certificate is not available", err.Error())
		return fmt.Errorf("certificate is not available: %w", err)
	}

	if cert == "" {
		hookContext.Commands.JujuLog(commands.Error, "Certificate is empty")
		return fmt.Errorf("certificate is empty")
	}

	roots := x509.NewCertPool()
	if ok := roots.AppendCertsFromPEM([]byte(cert)); !ok {
		return fmt.Errorf("failed to parse root certificate PEM")
	}

	clientConfig := &notary.Config{
		BaseURL: "https://127.0.0.1:" + fmt.Sprint(APIPort),
		TLSConfig: &tls.Config{
			RootCAs: roots,
		},
	}

	client, err := notary.New(clientConfig)
	if err != nil {
		return fmt.Errorf("could not create notary client: %w", err)
	}

	status, err := client.GetStatus()
	if err != nil {
		return fmt.Errorf("could not get status: %w", err)
	}

	if status.Initialized {
		return nil
	}

	password, err := getOrGenerateNotaryPassword(hookContext)
	if err != nil {
		return fmt.Errorf("could not get or generate password: %w", err)
	}

	if password == "" {
		return fmt.Errorf("could not get password from secret")
	}

	err = client.CreateAccount(&notary.CreateAccountOptions{
		Username: CharmAccountUsername,
		Password: password,
	})
	if err != nil {
		return fmt.Errorf("could not create account: %w", err)
	}

	return nil
}

func getOrGenerateNotaryPassword(hookContext *goops.HookContext) (string, error) {
	secret, _ := hookContext.Commands.SecretGet(&commands.SecretGetOptions{
		Refresh: true,
		Label:   NotaryLoginSecretLabel,
	})

	if secret != nil {
		return secret["password"], nil
	}

	password, err := generateRandomPassword()
	if err != nil {
		return "", fmt.Errorf("could not generate random password: %w", err)
	}

	secretAddOpts := &commands.SecretAddOptions{
		Label: NotaryLoginSecretLabel,
		Content: map[string]string{
			"password": password,
			"username": CharmAccountUsername,
		},
	}

	_, err = hookContext.Commands.SecretAdd(secretAddOpts)
	if err != nil {
		return "", fmt.Errorf("could not add secret: %w", err)
	}

	return password, nil
}

func generateRandomPassword() (string, error) {
	const passwordLength = 16

	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	b := make([]byte, passwordLength)

	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}

	for i := range b {
		b[i] = charset[b[i]%byte(len(charset))]
	}

	return string(b), nil
}

func getHostname(hookContext *goops.HookContext) string {
	modelName := hookContext.Environment.JujuModelName()
	unitName := hookContext.Environment.JujuUnitName()
	appName := strings.Split(unitName, "/")[0]
	unitNumber := strings.Split(unitName, "/")[1]
	unitHostname := fmt.Sprintf("%s-%s.%s-endpoints.%s.svc.cluster.local:%d", appName, unitNumber, appName, modelName, APIPort)

	return unitHostname
}
