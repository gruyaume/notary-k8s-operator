package certificates

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/gruyaume/goops"
	"github.com/gruyaume/goops/commands"
)

const (
	PrivateKeySecretLabel = "PRIVATE_KEY"
)

type CertificateRequestAttributes struct {
	CommonName          string
	SansDNS             []string
	SansIP              []string
	SansOID             []string
	EmailAddress        string
	Organization        string
	OrganizationalUnit  string
	CountryName         string
	StateOrProvinceName string
	LocalityName        string
}

type Integration struct {
	HookContext        *goops.HookContext
	RelationName       string
	CertificateRequest CertificateRequestAttributes
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

func (i *Integration) Request() error {
	relationID, err := i.GetRelationID()
	if err != nil {
		return fmt.Errorf("could not get relation ID: %v", err)
	}

	if i.certificateRequested() {
		i.HookContext.Commands.JujuLog(commands.Info, "Certificate already requested")
		return nil
	}

	privateKey, err := i.getOrGeneratePrivateKey()
	if err != nil {
		return fmt.Errorf("could not get or generate private key: %w", err)
	}

	csr, err := i.generateCSR(privateKey)
	if err != nil {
		return fmt.Errorf("could not generate CSR: %w", err)
	}

	csrMap := map[string]string{
		"certificate_signing_request": csr,
		"ca":                          "false",
	}

	csrsBytes, err := json.Marshal([]map[string]string{csrMap})
	if err != nil {
		return fmt.Errorf("could not marshal scrape metadata to JSON: %w", err)
	}

	relationData := map[string]string{
		"certificate_signing_requests": string(csrsBytes),
	}

	relationSetOpts := &commands.RelationSetOptions{
		ID:   relationID,
		App:  false,
		Data: relationData,
	}

	err = i.HookContext.Commands.RelationSet(relationSetOpts)
	if err != nil {
		return fmt.Errorf("could not set relation data: %w", err)
	}

	return nil
}

func (i *Integration) certificateRequested() bool {
	relationID, err := i.GetRelationID()
	if err != nil {
		i.HookContext.Commands.JujuLog(commands.Warning, "Could not get relation ID", err.Error())
		return false
	}

	unitName := i.HookContext.Environment.JujuUnitName()

	relationData, err := i.HookContext.Commands.RelationGet(&commands.RelationGetOptions{
		ID:     relationID,
		UnitID: unitName,
		App:    false,
	})
	if err != nil {
		return false
	}

	if relationData == nil {
		return false
	}

	certificateSigningRequestsStr := relationData["certificate_signing_requests"]
	if certificateSigningRequestsStr == "" {
		return false
	}

	var certificateSigningRequests []map[string]string

	err = json.Unmarshal([]byte(certificateSigningRequestsStr), &certificateSigningRequests)
	if err != nil {
		return false
	}

	if len(certificateSigningRequests) == 0 {
		return false
	}

	certificateSigningRequest := certificateSigningRequests[0]

	if certificateSigningRequest["certificate_signing_request"] == "" {
		return false
	}

	block, _ := pem.Decode([]byte(certificateSigningRequest["certificate_signing_request"]))
	if block == nil {
		return false
	}

	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return false
	}

	if csr.Subject.CommonName != i.CertificateRequest.CommonName {
		return false
	}

	if len(csr.DNSNames) != len(i.CertificateRequest.SansDNS) {
		return false
	}

	privateKey, err := i.GetPrivateKey()
	if err != nil {
		return false
	}

	block, _ = pem.Decode([]byte(privateKey))
	if block == nil {
		return false
	}

	err = csr.CheckSignature()

	return err == nil
}

type ProviderCertificate struct {
	CA                        string   `json:"ca"`
	Chain                     []string `json:"chain"`
	CertificateSigningRequest string   `json:"certificate_signing_request"`
	Certificate               string   `json:"certificate"`
}

func (i *Integration) GetProviderCertificate() ([]*ProviderCertificate, error) {
	relationID, err := i.GetRelationID()
	if err != nil {
		return nil, fmt.Errorf("could not get relation ID: %v", err)
	}

	relations, err := i.HookContext.Commands.RelationList(&commands.RelationListOptions{
		ID: relationID,
	})
	if err != nil {
		return nil, fmt.Errorf("could not list relations for ID %s: %v", relationID, err)
	}

	if len(relations) == 0 {
		return nil, fmt.Errorf("no relations found for ID %s", relationID)
	}

	relationData, err := i.HookContext.Commands.RelationGet(&commands.RelationGetOptions{
		ID:     relationID,
		UnitID: relations[0],
		App:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("could not get relation data: %w", err)
	}

	if relationData == nil {
		return nil, fmt.Errorf("relation data is empty")
	}

	certificatesStr := relationData["certificates"]
	if certificatesStr == "" {
		return nil, fmt.Errorf("no certificates found in relation data")
	}

	var providerCertificate []*ProviderCertificate

	err = json.Unmarshal([]byte(certificatesStr), &providerCertificate)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal provider certificate: %w", err)
	}

	return providerCertificate, nil
}

func (i *Integration) GetPrivateKey() (string, error) {
	secret, err := i.HookContext.Commands.SecretGet(&commands.SecretGetOptions{
		Label:   PrivateKeySecretLabel,
		Refresh: true,
	})
	if err != nil {
		return "", fmt.Errorf("cCould not get private key secret: %v", err)
	}

	if secret == nil {
		return "", fmt.Errorf("secret is empty")
	}

	return secret["private-key"], nil
}

func (i *Integration) getOrGeneratePrivateKey() (string, error) {
	secret, _ := i.HookContext.Commands.SecretGet(&commands.SecretGetOptions{
		Label:   PrivateKeySecretLabel,
		Refresh: true,
	})

	if secret != nil {
		return secret["private-key"], nil
	}

	i.HookContext.Commands.JujuLog(commands.Warning, "Secret is empty")

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", fmt.Errorf("failed to generate private key: %w", err)
	}

	i.HookContext.Commands.JujuLog(commands.Warning, "Generated new private key")

	keyBuf := &bytes.Buffer{}
	privBytes := x509.MarshalPKCS1PrivateKey(priv)

	err = pem.Encode(keyBuf, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: privBytes})
	if err != nil {
		return "", fmt.Errorf("failed to PEM‐encode private key: %w", err)
	}

	secretAddOpts := &commands.SecretAddOptions{
		Label: PrivateKeySecretLabel,
		Content: map[string]string{
			"private-key": keyBuf.String(),
		},
	}

	_, err = i.HookContext.Commands.SecretAdd(secretAddOpts)
	if err != nil {
		return "", fmt.Errorf("could not add secret: %w", err)
	}

	return keyBuf.String(), nil
}

func (i *Integration) generateCSR(privateKeyPEM string) (string, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return "", fmt.Errorf("failed to PEM decode private key")
	}

	privKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("could not parse private key: %v", err)
	}

	template := x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:         i.CertificateRequest.CommonName,
			Organization:       []string{i.CertificateRequest.Organization},
			OrganizationalUnit: []string{i.CertificateRequest.OrganizationalUnit},
			Country:            []string{i.CertificateRequest.CountryName},
			Province:           []string{i.CertificateRequest.StateOrProvinceName},
			Locality:           []string{i.CertificateRequest.LocalityName},
		},
		DNSNames:       i.CertificateRequest.SansDNS,
		EmailAddresses: []string{i.CertificateRequest.EmailAddress},
	}

	for _, ipStr := range i.CertificateRequest.SansIP {
		if ip := net.ParseIP(ipStr); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		}
	}

	for _, oidStr := range i.CertificateRequest.SansOID {
		parts := strings.Split(oidStr, ".")

		var oid asn1.ObjectIdentifier

		for _, p := range parts {
			v, err := strconv.Atoi(p)
			if err != nil {
				return "", fmt.Errorf("invalid OID %q: %w", oidStr, err)
			}

			oid = append(oid, v)
		}

		template.ExtraExtensions = append(template.ExtraExtensions, pkix.Extension{
			Id:    oid,
			Value: nil, // if you need a specific value, marshal it here
		})
	}

	derCSR, err := x509.CreateCertificateRequest(rand.Reader, &template, privKey)
	if err != nil {
		return "", fmt.Errorf("failed to create CSR: %w", err)
	}

	var pemBuf bytes.Buffer
	if err := pem.Encode(&pemBuf, &pem.Block{Type: "CERTIFICATE REQUEST", Bytes: derCSR}); err != nil {
		return "", fmt.Errorf("failed to PEM‐encode CSR: %w", err)
	}

	return pemBuf.String(), nil
}
