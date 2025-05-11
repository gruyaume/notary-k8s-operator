package notary

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
)

type CertificateRequest struct {
	ID               int    `json:"id"`
	CSR              string `json:"csr"`
	CertificateChain string `json:"certificate_chain"`
	Status           string `json:"status"`
}

func (c *Client) ListCertificateRequests() ([]*CertificateRequest, error) {
	resp, err := c.Requester.Do(context.Background(), &RequestOptions{
		Type:   SyncRequest,
		Method: "GET",
		Path:   "api/v1/certificate_requests",
	})
	if err != nil {
		return nil, err
	}

	var certRequestResponse []*CertificateRequest

	err = resp.DecodeResult(&certRequestResponse)
	if err != nil {
		return nil, err
	}

	return certRequestResponse, nil
}

type CreateCertificateRequestOptions struct {
	CSR string `json:"csr"`
}

func (c *Client) RequestCertificate(opts *CreateCertificateRequestOptions) error {
	payload := struct {
		CSR string `json:"csr"`
	}{
		CSR: opts.CSR,
	}

	var body bytes.Buffer

	err := json.NewEncoder(&body).Encode(payload)
	if err != nil {
		return err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	_, err = c.Requester.Do(context.Background(), &RequestOptions{
		Type:    SyncRequest,
		Method:  "POST",
		Path:    "api/v1/certificate_requests",
		Body:    &body,
		Headers: headers,
	})
	if err != nil {
		return err
	}

	return nil
}

func Serialize(pemString string) []string {
	parts := bytes.Split([]byte(pemString), []byte("-----END CERTIFICATE-----"))

	var serialized []string

	for _, part := range parts {
		trimmed := strings.Trim(string(part), "\n\r\t ")
		if trimmed == "" {
			continue
		}

		serialized = append(serialized, trimmed+"-----END CERTIFICATE-----")
	}

	return serialized
}
