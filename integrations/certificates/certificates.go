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

func (i *Integration) Request() error {
	relationIDs, err := i.HookContext.Commands.RelationIDs(&commands.RelationIDsOptions{
		Name: i.RelationName,
	})
	if err != nil {
		return fmt.Errorf("could not get relation IDs: %w", err)
	}

	if len(relationIDs) == 0 {
		return fmt.Errorf("no relation IDs found for %s", i.RelationName)
	}

	privateKey, err := i.getOrGeneratePrivateKey()
	if err != nil {
		return fmt.Errorf("could not get or generate private key: %w", err)
	}

	csr, err := i.generateCSR(privateKey)
	if err != nil {
		return fmt.Errorf("could not generate CSR: %w", err)
	}

	csrsBytes, err := json.Marshal(csr)
	if err != nil {
		return fmt.Errorf("could not marshal scrape metadata to JSON: %w", err)
	}

	relationData := map[string]string{
		"certificate_signing_requests": string(csrsBytes),
	}

	relationSetOpts := &commands.RelationSetOptions{
		ID:   relationIDs[0],
		App:  false,
		Data: relationData,
	}

	err = i.HookContext.Commands.RelationSet(relationSetOpts)
	if err != nil {
		return fmt.Errorf("could not set relation data: %w", err)
	}

	return nil
}

type ProviderCertificate struct {
	RelationID  int
	Certificate string
	CA          string
	Chain       []string
}

func (i *Integration) GetCertificate() (ProviderCertificate, error) {
	return ProviderCertificate{}, nil
}

func (i *Integration) getOrGeneratePrivateKey() (string, error) {
	secret, _ := i.HookContext.Commands.SecretGet(&commands.SecretGetOptions{
		Label:   PrivateKeySecretLabel,
		Refresh: true,
	})

	if secret != nil {
		return secret["private-key"], nil
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", fmt.Errorf("failed to generate private key: %w", err)
	}

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
	// 1) Decode PEM
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return "", fmt.Errorf("failed to PEM decode private key")
	}

	// 2) Parse RSA key (PKCS#1 or fallback to PKCS#8)
	privKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		parsed, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return "", fmt.Errorf("failed to parse private key: %v / %v", err, err2)
		}
		var ok bool
		privKey, ok = parsed.(*rsa.PrivateKey)
		if !ok {
			return "", fmt.Errorf("parsed private key is not RSA")
		}
	}

	// 3) Build the CSR template
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

	// 4) Add IP SANs
	for _, ipStr := range i.CertificateRequest.SansIP {
		if ip := net.ParseIP(ipStr); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		}
	}

	// 5) Add any OID‐based SANs as extra extensions
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

	// 6) Create the CSR (DER)
	derCSR, err := x509.CreateCertificateRequest(rand.Reader, &template, privKey)
	if err != nil {
		return "", fmt.Errorf("failed to create CSR: %w", err)
	}

	// 7) PEM‐encode the CSR
	var pemBuf bytes.Buffer
	if err := pem.Encode(&pemBuf, &pem.Block{Type: "CERTIFICATE REQUEST", Bytes: derCSR}); err != nil {
		return "", fmt.Errorf("failed to PEM‐encode CSR: %w", err)
	}

	return pemBuf.String(), nil
}
