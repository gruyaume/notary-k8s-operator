package charm

import (
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

func setPorts(hookContext *goops.HookContext) error {
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

func HandleDefaultHook(hookContext *goops.HookContext) {
	isLeader, err := hookContext.Commands.IsLeader()
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not check if unit is leader:", err.Error())
		return
	}

	if !isLeader {
		hookContext.Commands.JujuLog(commands.Warning, "Unit is not leader")
		return
	}

	metadata, err := metadata.GetCharmMetadata(hookContext.Environment)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not get charm metadata:", err.Error())
		return
	}

	prometheusIntegration := &prometheus.Integration{
		HookContext:  hookContext,
		RelationName: MetricsIntegrationName,
		CharmName:    metadata.Name,
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

	err = prometheusIntegration.Write()
	if err != nil {
		hookContext.Commands.JujuLog(commands.Info, "Could not write prometheus integration:", err.Error())
	}

	err = setPorts(hookContext)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not set ports:", err.Error())
		return
	}

	hookContext.Commands.JujuLog(commands.Info, "Ports set")

	pebble, err := client.New(&client.Config{Socket: socketPath})
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not connect to pebble:", err.Error())
		return
	}

	expectedConfig, err := getExpectedConfig()
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not get expected config:", err.Error())
		return
	}

	err = pushFile(pebble, string(expectedConfig), "/etc/notary/config/notary.yaml")
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not push config file:", err.Error())
		return
	}

	hookContext.Commands.JujuLog(commands.Info, "Config file pushed")

	changed, err := syncCertificate(hookContext, pebble)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not sync certificate:", err.Error())
		return
	}

	err = addPebbleLayer(pebble)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not add pebble layer:", err.Error())
		return
	}

	if changed {
		err := restartPebbleService(pebble)
		if err != nil {
			hookContext.Commands.JujuLog(commands.Error, "Could not restart pebble service:", err.Error())
			return
		}

		hookContext.Commands.JujuLog(commands.Info, "Pebble service restarted")
	}

	hookContext.Commands.JujuLog(commands.Info, "Pebble layer added")

	err = startPebbleService(pebble)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not start pebble service:", err.Error())
		return
	}

	hookContext.Commands.JujuLog(commands.Info, "Pebble service started")

	cert, err := getFileContent(pebble, CertPath)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Certificate is not available", err.Error())
		return
	}

	if cert == "" {
		hookContext.Commands.JujuLog(commands.Error, "Certificate is empty")
		return
	}

	err = createAdminAccount(hookContext, cert)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not create admin account", err.Error())
		return
	}

	hookContext.Commands.JujuLog(commands.Info, "Admin account created")
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

func syncCertificate(hookContext *goops.HookContext, pebble *client.Client) (bool, error) {
	var changed bool

	var err error

	if !tlsIntegrationCreated(hookContext) {
		hookContext.Commands.JujuLog(commands.Info, "TLS integration not created")

		changed, err = syncSelfSignedCertificate(hookContext, pebble)
		if err != nil {
			return false, fmt.Errorf("could not sync self signed certificate: %v", err)
		}
	} else {
		changed, err = syncTlsProviderCertificate(hookContext, pebble)
		if err != nil {
			return false, fmt.Errorf("could not sync tls provider certificate: %v", err)
		}
	}

	return changed, nil
}

func SetStatus(hookContext *goops.HookContext) {
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

func createAdminAccount(hookContext *goops.HookContext, certPEM string) error {
	roots := x509.NewCertPool()
	if ok := roots.AppendCertsFromPEM([]byte(certPEM)); !ok {
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
