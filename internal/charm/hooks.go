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

	prometheusIntegration := &prometheus.Integration{
		HookContext:  hookContext,
		RelationName: MetricsIntegrationName,
		CharmName:    "notary-k8s",
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

	cert, err := syncCertificate(hookContext, pebble)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not sync certificate:", err.Error())
		return
	}

	err = addPebbleLayer(pebble)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not add pebble layer:", err.Error())
		return
	}

	hookContext.Commands.JujuLog(commands.Info, "Pebble layer added")

	err = startPebbleService(pebble)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not start pebble service:", err.Error())
		return
	}

	hookContext.Commands.JujuLog(commands.Info, "Pebble service started")

	err = createAdminAccount(hookContext, cert)
	if err != nil {
		hookContext.Commands.JujuLog(commands.Error, "Could not create admin account:", err.Error())
		return
	}

	hookContext.Commands.JujuLog(commands.Info, "Admin account created")
}

func syncCertificate(hookContext *goops.HookContext, pebble *client.Client) (string, error) {
	certContent, _ := getFileContent(pebble, CertPath)

	if certContent != "" {
		hookContext.Commands.JujuLog(commands.Info, "Certificate already exists, skipping generation")
		return certContent, nil
	}

	cert, key, err := generateCertificate()
	if err != nil {
		return "", fmt.Errorf("could not generate certificate: %w", err)
	}

	hookContext.Commands.JujuLog(commands.Info, "Certificate generated")

	err = pushFile(pebble, cert, "/etc/notary/config/cert.pem")
	if err != nil {
		return "", fmt.Errorf("could not push certificate: %w", err)
	}

	hookContext.Commands.JujuLog(commands.Info, "Certificate pushed")

	err = pushFile(pebble, key, "/etc/notary/config/key.pem")
	if err != nil {
		return "", fmt.Errorf("could not push key: %w", err)
	}

	hookContext.Commands.JujuLog(commands.Info, "Key pushed")

	return cert, nil
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

	// check if secret already exists
	secret, _ := hookContext.Commands.SecretGet(&commands.SecretGetOptions{
		Label: NotaryLoginSecretLabel,
	})

	var password string

	if secret == nil {
		password, err := generateRandomPassword()
		if err != nil {
			return fmt.Errorf("could not generate random password: %w", err)
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
			return fmt.Errorf("could not add secret: %w", err)
		}
	} else {
		password = secret["password"]
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
