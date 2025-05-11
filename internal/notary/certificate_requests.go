package notary

import "context"

type CertificateRequest struct {
	ID               string `json:"id"`
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
