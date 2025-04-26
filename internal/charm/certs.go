package charm

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

func generateCertificate() (certPEM string, keyPEM string, err error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate private key: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)

	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Example Co"},
			CommonName:   "127.0.0.1",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour), // 1 year
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	derCert, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return "", "", fmt.Errorf("failed to create certificate: %w", err)
	}

	certBuf := &bytes.Buffer{}

	err = pem.Encode(certBuf, &pem.Block{Type: "CERTIFICATE", Bytes: derCert})
	if err != nil {
		return "", "", fmt.Errorf("failed to PEM‐encode certificate: %w", err)
	}

	keyBuf := &bytes.Buffer{}
	privBytes := x509.MarshalPKCS1PrivateKey(priv)

	err = pem.Encode(keyBuf, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: privBytes})
	if err != nil {
		return "", "", fmt.Errorf("failed to PEM‐encode private key: %w", err)
	}

	return certBuf.String(), keyBuf.String(), nil
}
