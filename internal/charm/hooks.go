package charm

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/canonical/pebble/client"
	"github.com/gruyaume/charm-libraries/certificates"
	"github.com/gruyaume/charm-libraries/logging"
	"github.com/gruyaume/charm-libraries/prometheus"
	"github.com/gruyaume/goops"
	"github.com/gruyaume/notary-k8s-operator/internal/notary"
)

const (
	KeyPath                    = "/etc/notary/config/key.pem"
	CertPath                   = "/etc/notary/config/cert.pem"
	ConfigPath                 = "/etc/notary/config/notary.yaml"
	APIPort                    = 2111
	CharmAccountUsername       = "charm@notary.com"
	NotaryLoginSecretLabel     = "NOTARY_LOGIN"
	MetricsIntegrationName     = "metrics"
	LoggingIntegrationName     = "logging"
	TLSRequiresIntegrationName = "access-certificates"
	TLSProvidesIntegrationName = "certificates"
)

func Configure() error {
	isLeader, err := goops.IsLeader()
	if err != nil {
		return fmt.Errorf("could not check if unit is leader: %w", err)
	}

	if !isLeader {
		_ = goops.SetUnitStatus(goops.StatusBlocked, "Unit is not leader")
		return nil
	}

	err = writePrometheus()
	if err != nil {
		return fmt.Errorf("could not write prometheus integration: %w", err)
	}

	err = goops.SetPorts([]*goops.Port{
		{
			Port:     APIPort,
			Protocol: "tcp",
		},
	})
	if err != nil {
		return fmt.Errorf("could not set ports: %w", err)
	}

	pebble := goops.Pebble("notary")

	err = syncConfig(pebble)
	if err != nil {
		return fmt.Errorf("could not sync config: %w", err)
	}

	changed, err := syncAccessCertificate(pebble)
	if err != nil {
		return fmt.Errorf("could not sync access certificate: %w", err)
	}

	err = syncPebbleService(pebble, changed)
	if err != nil {
		return fmt.Errorf("could not sync pebble service: %w", err)
	}

	configureLogging()

	err = createAdminAccount(pebble)
	if err != nil {
		return fmt.Errorf("could not create admin account: %w", err)
	}

	notaryClient, err := getLoggedInNotaryClient(pebble)
	if err != nil {
		return fmt.Errorf("could not get logged in notary client: %w", err)
	}

	if notaryClient == nil {
		return fmt.Errorf("notary client is nil, cannot proceed")
	}

	err = syncCertificatesProvides(notaryClient)
	if err != nil {
		return fmt.Errorf("could not sync certificates provides: %w", err)
	}

	_ = goops.SetUnitStatus(goops.StatusActive, "")

	return nil
}

func getLoggedInNotaryClient(pebble goops.PebbleClient) (*notary.Client, error) {
	cert, err := getFileContent(pebble, CertPath)
	if err != nil {
		return nil, fmt.Errorf("certificate is not available: %w", err)
	}

	if cert == "" {
		return nil, fmt.Errorf("certificate is empty")
	}

	notaryClient, err := NewNotaryClient(cert)
	if err != nil {
		return nil, fmt.Errorf("could not create notary client: %w", err)
	}

	err = loginNotaryClient(notaryClient)
	if err != nil {
		return nil, fmt.Errorf("could not login to notary client: %w", err)
	}

	goops.LogInfof("Logged in to Notary client")

	return notaryClient, nil
}

func writePrometheus() error {
	meta, err := goops.ReadMetadata()
	if err != nil {
		return fmt.Errorf("could not read metadata: %w", err)
	}

	prometheusIntegration := &prometheus.Integration{
		RelationName: MetricsIntegrationName,
		CharmName:    meta.Name,
		Jobs: []*prometheus.Job{
			{
				Scheme:      "https",
				TLSConfig:   prometheus.TLSConfig{InsecureSkipVerify: true},
				MetricsPath: "/metrics",
				StaticConfigs: []prometheus.StaticConfig{
					{
						Targets: []string{getHostname()},
					},
				},
			},
		},
	}

	err = prometheusIntegration.Write()
	if err != nil {
		goops.LogDebugf("Could not write prometheus integration, this may happen if relation is not created: %v", err)
		return nil
	}

	goops.LogInfof("Prometheus integration written for %s", prometheusIntegration.RelationName)

	return nil
}

func configureLogging() {
	i := &logging.Integration{
		RelationName:  "logging",
		ContainerName: "notary",
	}

	err := i.EnableEndpoints()
	if err != nil {
		goops.LogDebugf("Could not enable logging endpoints: %v", err)
		return
	}
}

func syncConfig(pebble goops.PebbleClient) error {
	expectedConfig, err := getExpectedConfig()
	if err != nil {
		return fmt.Errorf("could not get expected config: %w", err)
	}

	err = pushFile(pebble, string(expectedConfig), "/etc/notary/config/notary.yaml")
	if err != nil {
		return fmt.Errorf("could not push config file: %w", err)
	}

	goops.LogInfof("Config file pushed to %s", ConfigPath)

	return nil
}

func syncPebbleService(pebble goops.PebbleClient, restart bool) error {
	if !pebbleLayerCreated(pebble) {
		goops.LogInfof("Pebble layer not created")

		err := addPebbleLayer(pebble)
		if err != nil {
			return fmt.Errorf("could not add pebble layer: %w", err)
		}

		goops.LogInfof("Pebble layer created")
	}

	if restart {
		_, err := pebble.Restart(&client.ServiceOptions{
			Names: []string{"notary"},
		})
		if err != nil {
			return fmt.Errorf("could not restart pebble service: %w", err)
		}

		goops.LogInfof("Pebble service restarted")
	}

	_, err := pebble.Start(&client.ServiceOptions{
		Names: []string{"notary"},
	})
	if err != nil {
		return fmt.Errorf("could not start pebble service: %w", err)
	}

	goops.LogInfof("Pebble service started")

	return nil
}

// syncCertificatesProvides provides TLS certificates to TLS requirers.
func syncCertificatesProvides(notaryClient *notary.Client) error {
	if !integrationCreated(TLSProvidesIntegrationName) {
		return nil
	}

	provider := certificates.IntegrationProvider{
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
			goops.LogInfof("No matching notary certificate request found for databag certificate request %s", dr.RelationID)

			err := notaryClient.RequestCertificate(&notary.CreateCertificateRequestOptions{CSR: csr})
			if err != nil {
				return fmt.Errorf("could not request certificate: %w", err)
			}

			goops.LogInfof("Certificate request sent to notary for relation %s", dr.RelationID)
		case 1: // One matching Certificate Request in Notary
			nr := matches[0]
			if nr.Status != "Active" {
				goops.LogDebugf("Notary certificate request for relation %s is not active, skipping", dr.RelationID)
				continue
			}

			if provider.AlreadyProvided(dr.RelationID, csr) {
				continue
			}

			if err := sendCertificate(provider, dr.RelationID, nr); err != nil {
				return fmt.Errorf("could not set relation certificate: %w", err)
			}

			goops.LogInfof("Relation certificate set for relation %s", dr.RelationID)

		default: // Multiple matching Certificate Requests in Notary
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

func loginNotaryClient(client *notary.Client) error {
	secret, err := goops.GetSecretByLabel(NotaryLoginSecretLabel, false, true)
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

func integrationCreated(name string) bool {
	relationIDs, err := goops.GetRelationIDs(name)
	if err != nil {
		return false
	}

	if len(relationIDs) == 0 {
		return false
	}

	return true
}

func syncSelfSignedCertificate(pebble goops.PebbleClient) (bool, error) {
	certContent, _ := getFileContent(pebble, CertPath)

	if certContent != "" {
		goops.LogInfof("Certificate already exists, skipping generation")
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

	goops.LogInfof("Certificate generated")

	err = pushFile(pebble, cert, "/etc/notary/config/cert.pem")
	if err != nil {
		return false, fmt.Errorf("could not push certificate: %w", err)
	}

	goops.LogInfof("Certificate pushed")

	err = pushFile(pebble, key, "/etc/notary/config/key.pem")
	if err != nil {
		return false, fmt.Errorf("could not push key: %w", err)
	}

	goops.LogInfof("Key pushed")

	return true, nil
}

// syncTlsProviderCertificate makes a certificate request to the TLS provider
// and pushes the certificate and key to the pebble client.
func syncTlsProviderCertificate(pebble goops.PebbleClient) (bool, error) {
	changed := false
	tlsRequirerIntegration := certificates.IntegrationRequirer{
		RelationName: TLSRequiresIntegrationName,
		CertificateRequest: certificates.CertificateRequestAttributes{
			CommonName:          getHostname(),
			SansDNS:             []string{getHostname()},
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

	goops.LogInfof("Certificate requested")

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

	goops.LogInfof("Certificate received")

	privateKey, err := tlsRequirerIntegration.GetPrivateKey()
	if err != nil {
		return changed, fmt.Errorf("could not get private key: %w", err)
	}

	existingPrivateKey, _ := getFileContent(pebble, KeyPath)

	if existingPrivateKey != privateKey {
		goops.LogWarningf("Private key is different")

		err = pushFile(pebble, privateKey, KeyPath)
		if err != nil {
			return changed, fmt.Errorf("could not push key: %w", err)
		}

		goops.LogInfof("Key pushed")

		changed = true
	}

	existingCertificate, _ := getFileContent(pebble, CertPath)
	if existingCertificate != providerCert[0].Certificate {
		goops.LogWarningf("Certificate is different, existing: %s, new: %s", existingCertificate, providerCert[0].Certificate)

		err = pushFile(pebble, providerCert[0].Certificate, CertPath)
		if err != nil {
			return changed, fmt.Errorf("could not push certificate: %w", err)
		}

		goops.LogInfof("Certificate pushed")

		changed = true
	}

	return changed, nil
}

func syncAccessCertificate(pebble goops.PebbleClient) (bool, error) {
	var changed bool

	var err error

	if !integrationCreated(TLSRequiresIntegrationName) {
		goops.LogInfof("`%s` integration not created, using self-signed certificate", TLSRequiresIntegrationName)

		changed, err = syncSelfSignedCertificate(pebble)
		if err != nil {
			return false, fmt.Errorf("could not sync self signed certificate: %v", err)
		}
	} else {
		changed, err = syncTlsProviderCertificate(pebble)
		if err != nil {
			return false, fmt.Errorf("could not sync tls provider certificate: %v", err)
		}
	}

	goops.LogInfof("Synced TLS certificate, changed: %v", changed)

	return changed, nil
}

func createAdminAccount(pebble goops.PebbleClient) error {
	cert, err := getFileContent(pebble, CertPath)
	if err != nil {
		return fmt.Errorf("certificate is not available: %w", err)
	}

	if cert == "" {
		return fmt.Errorf("certificate is empty")
	}

	notaryClient, err := NewNotaryClient(cert)
	if err != nil {
		return fmt.Errorf("could not create notary client: %w", err)
	}

	status, err := notaryClient.GetStatus()
	if err != nil {
		return fmt.Errorf("could not get status: %w", err)
	}

	if status.Initialized {
		return nil
	}

	password, err := getOrGenerateNotaryPassword()
	if err != nil {
		return fmt.Errorf("could not get or generate password: %w", err)
	}

	if password == "" {
		return fmt.Errorf("could not get password from secret")
	}

	err = notaryClient.CreateAccount(&notary.CreateAccountOptions{
		Username: CharmAccountUsername,
		Password: password,
	})
	if err != nil {
		return fmt.Errorf("could not create account: %w", err)
	}

	goops.LogInfof("Account created for username: %s", CharmAccountUsername)

	return nil
}

func getOrGenerateNotaryPassword() (string, error) {
	secret, _ := goops.GetSecretByLabel(NotaryLoginSecretLabel, false, true)

	if secret != nil {
		return secret["password"], nil
	}

	password, err := generateRandomPassword()
	if err != nil {
		return "", fmt.Errorf("could not generate random password: %w", err)
	}

	secretAddOpts := &goops.AddSecretOptions{
		Label: NotaryLoginSecretLabel,
		Content: map[string]string{
			"password": password,
			"username": CharmAccountUsername,
		},
	}

	_, err = goops.AddSecret(secretAddOpts)
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

func getHostname() string {
	env := goops.ReadEnv()

	appName := strings.Split(env.UnitName, "/")[0]
	unitNumber := strings.Split(env.UnitName, "/")[1]
	unitHostname := fmt.Sprintf("%s-%s.%s-endpoints.%s.svc.cluster.local:%d", appName, unitNumber, appName, env.ModelName, APIPort)

	return unitHostname
}
