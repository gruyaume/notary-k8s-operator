package charm

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/canonical/pebble/client"
	"github.com/gruyaume/charm-libraries/certificates"
	"github.com/gruyaume/charm-libraries/prometheus"
	"github.com/gruyaume/goops"
	"github.com/gruyaume/goops/commands"
	"github.com/gruyaume/goops/metadata"
	"github.com/gruyaume/notary-k8s-operator/internal/notary"
	"go.opentelemetry.io/otel"
)

const (
	KeyPath                    = "/etc/notary/config/key.pem"
	CertPath                   = "/etc/notary/config/cert.pem"
	ConfigPath                 = "/etc/notary/config/notary.yaml"
	APIPort                    = 2111
	CharmAccountUsername       = "charm@notary.com"
	NotaryLoginSecretLabel     = "NOTARY_LOGIN"
	MetricsIntegrationName     = "metrics"
	TLSRequiresIntegrationName = "access-certificates"
	TLSProvidesIntegrationName = "certificates"
)

func setPorts(ctx context.Context, hookContext *goops.HookContext) error {
	_, span := otel.Tracer("notary-k8s").Start(ctx, "Set Ports")
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
		return fmt.Errorf("could not set ports: %w", err)
	}

	return nil
}

func HandleDefaultHook(ctx context.Context, hookContext *goops.HookContext) {
	ctx, span := otel.Tracer("notary-k8s").Start(ctx, "Handle DefaultHook")
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

	changed, err := syncAccessCertificate(ctx, hookContext, pebble)
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

	notaryClient := getLoggedInNotaryClient(hookContext, pebble)
	if notaryClient == nil {
		return
	}

	err = syncCertificatesProvides(hookContext, notaryClient)
	if err != nil {
		return
	}
}

func getLoggedInNotaryClient(hookContext *goops.HookContext, pebble *client.Client) *notary.Client {
	cert, err := getFileContent(pebble, CertPath)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Certificate is not available", err.Error())
		return nil
	}

	if cert == "" {
		hookContext.Commands.JujuLog(commands.Error, "Certificate is empty")
		return nil
	}

	notaryClient, err := NewNotaryClient(cert)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not create notary client:", err.Error())
		return nil
	}

	err = loginNotaryClient(hookContext, notaryClient)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not login to Notary client", err.Error())
		return nil
	}

	hookContext.Commands.JujuLog(commands.Info, "Logged in to notary")

	return notaryClient
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
	_, span := otel.Tracer("notary-k8s").Start(ctx, "Sync Config")
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
	_, span := otel.Tracer("notary-k8s").Start(ctx, "sync PebbleService")
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

// syncCertificatesProvides provides TLS certificates to TLS requirers.
func syncCertificatesProvides(hookContext *goops.HookContext, notaryClient *notary.Client) error {
	if !integrationCreated(hookContext, TLSProvidesIntegrationName) {
		return nil
	}

	provider := certificates.IntegrationProvider{
		HookContext:  hookContext,
		RelationName: TLSProvidesIntegrationName,
	}

	databagReqs, err := provider.GetOutstandingCertificateRequests()
	if err != nil {
		return fmt.Errorf("could not list databag certificate requests: %w", err)
	}

	notaryReqs, err := notaryClient.ListCertificateRequests()
	if err != nil {
		return fmt.Errorf("could not list notary certificate requests: %w", err)
	}

	for _, dr := range databagReqs {
		csr := dr.CertificateSigningRequest.Raw
		matches := findNotaryRequestsByCSR(csr, notaryReqs)

		switch len(matches) {
		case 0: // No matching Certificate Request in Notary
			hookContext.Commands.JujuLog(commands.Info, "No matching notary certificate request found; sending new request")

			err := notaryClient.RequestCertificate(&notary.CreateCertificateRequestOptions{CSR: csr})
			if err != nil {
				hookContext.Commands.JujuLog(commands.Error, "Could not request certificate:", err.Error())
				return fmt.Errorf("could not request certificate: %w", err)
			}

			hookContext.Commands.JujuLog(commands.Info, "Certificate request sent to notary")

		case 1: // One matching Certificate Request in Notary
			nr := matches[0]
			if nr.Status != "Active" {
				hookContext.Commands.JujuLog(commands.Debug, "Notary certificate request is not active")
				continue
			}

			if provider.AlreadyProvided(dr.RelationID, csr) {
				continue
			}

			if err := sendCertificate(provider, dr.RelationID, nr); err != nil {
				hookContext.Commands.JujuLog(commands.Error,
					"Could not set relation certificate:", err.Error())
				return fmt.Errorf("could not set relation certificate: %w", err)
			}

			hookContext.Commands.JujuLog(commands.Info, "Relation certificate set")

		default: // Multiple matching Certificate Requests in Notary
			hookContext.Commands.JujuLog(commands.Error,
				"Multiple notary certificate requests found for databag certificate request")
			return fmt.Errorf("multiple notary certificate requests found for databag certificate request")
		}
	}

	return nil
}

// findNotaryRequestsByCSR returns all Notary requests whose CSR exactly matches.
func findNotaryRequestsByCSR(csr string, reqs []*notary.CertificateRequest) []*notary.CertificateRequest {
	var out []*notary.CertificateRequest

	for _, r := range reqs {
		if r.CSR == csr {
			out = append(out, r)
		}
	}

	return out
}

func sendCertificate(
	provider certificates.IntegrationProvider,
	relationID string,
	req *notary.CertificateRequest,
) error {
	chain := notary.Serialize(req.CertificateChain)
	opts := &certificates.SetRelationCertificateOptions{
		RelationID:                relationID,
		CA:                        chain[1],
		Chain:                     chain,
		CertificateSigningRequest: req.CSR,
		Certificate:               chain[0],
	}

	return provider.SetRelationCertificate(opts)
}

func NewNotaryClient(certPEM string) (*notary.Client, error) {
	roots := x509.NewCertPool()
	if ok := roots.AppendCertsFromPEM([]byte(certPEM)); !ok {
		return nil, fmt.Errorf("invalid root cert PEM")
	}

	cfg := &notary.Config{
		BaseURL:   fmt.Sprintf("https://127.0.0.1:%d", APIPort),
		TLSConfig: &tls.Config{RootCAs: roots},
	}

	return notary.New(cfg)
}

func loginNotaryClient(hookContext *goops.HookContext, client *notary.Client) error {
	secret, err := hookContext.Commands.SecretGet(&commands.SecretGetOptions{
		Refresh: true,
		Label:   NotaryLoginSecretLabel,
	})
	if err != nil {
		return fmt.Errorf("could not get secret: %w", err)
	}

	if secret == nil {
		return fmt.Errorf("secret is empty")
	}

	password := secret["password"]
	if password == "" {
		return fmt.Errorf("password is empty")
	}

	err = client.Login(&notary.LoginOptions{
		Username: CharmAccountUsername,
		Password: password,
	})
	if err != nil {
		return fmt.Errorf("could not login to notary: %w", err)
	}

	return nil
}

func integrationCreated(hookContext *goops.HookContext, name string) bool {
	relationIDs, err := hookContext.Commands.RelationIDs(&commands.RelationIDsOptions{
		Name: name,
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

	cert, key, err := certificates.GenerateCertificate(&certificates.GenerateCertificateOpts{
		CommonName:       "127.0.0.1",
		ValidityDuration: 365 * 24 * time.Hour,
		SANIPAddresses:   []net.IP{net.ParseIP("127.0.0.1")},
	})
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

// syncTlsProviderCertificate makes a certificate request to the TLS provider
// and pushes the certificate and key to the pebble client.
func syncTlsProviderCertificate(hookContext *goops.HookContext, pebble *client.Client) (bool, error) {
	changed := false
	tlsRequirerIntegration := certificates.IntegrationRequirer{
		HookContext:  hookContext,
		RelationName: TLSRequiresIntegrationName,
		CertificateRequest: certificates.CertificateRequestAttributes{
			CommonName:          getHostname(hookContext),
			SansDNS:             []string{getHostname(hookContext)},
			SansIP:              []string{"127.0.0.1"},
			CountryName:         "CA",
			StateOrProvinceName: "QC",
			LocalityName:        "Montreal",
		},
	}

	err := tlsRequirerIntegration.Request()
	if err != nil {
		return changed, fmt.Errorf("could not request certificate: %w", err)
	}

	hookContext.Commands.JujuLog(commands.Info, "Certificate requested")

	providerCert, err := tlsRequirerIntegration.GetProviderCertificate()
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

	privateKey, err := tlsRequirerIntegration.GetPrivateKey()
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

func syncAccessCertificate(ctx context.Context, hookContext *goops.HookContext, pebble *client.Client) (bool, error) {
	_, span := otel.Tracer("notary-k8s").Start(ctx, "Sync AccessCertificate")
	defer span.End()

	var changed bool

	var err error

	if !integrationCreated(hookContext, TLSRequiresIntegrationName) {
		hookContext.Commands.JujuLog(commands.Info, "`access-certificates` integration not created")

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
	_, span := otel.Tracer("notary-k8s").Start(ctx, "Create AdminAccount")
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

	notaryClient, err := NewNotaryClient(cert)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not create notary client:", err.Error())
		return fmt.Errorf("could not create notary client: %w", err)
	}

	status, err := notaryClient.GetStatus()
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not get status:", err.Error())
		return fmt.Errorf("could not get status: %w", err)
	}

	if status.Initialized {
		return nil
	}

	password, err := getOrGenerateNotaryPassword(hookContext)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not get or generate password:", err.Error())
		return fmt.Errorf("could not get or generate password: %w", err)
	}

	if password == "" {
		hookContext.Commands.JujuLog(commands.Error, "Password is empty")
		return fmt.Errorf("could not get password from secret")
	}

	err = notaryClient.CreateAccount(&notary.CreateAccountOptions{
		Username: CharmAccountUsername,
		Password: password,
	})
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not create account:", err.Error())
		return fmt.Errorf("could not create account: %w", err)
	}

	hookContext.Commands.JujuLog(commands.Info, "Account created")

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
