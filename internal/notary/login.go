package notary

import (
	"bytes"
	"context"
	"encoding/json"
)

type LoginOptions struct {
	Username string
	Password string
}

type LoginResponseResult struct {
	Token string `json:"token"`
}

// Login authenticates the user with the provided username and password.
// It stores the token in the client for future requests.
func (c *Client) Login(opts *LoginOptions) error {
	payload := struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{
		Username: opts.Username,
		Password: opts.Password,
	}

	var body bytes.Buffer

	err := json.NewEncoder(&body).Encode(payload)
	if err != nil {
		return err
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	resp, err := c.Requester.Do(context.Background(), &RequestOptions{
		Type:    SyncRequest,
		Method:  "POST",
		Path:    "login",
		Body:    &body,
		Headers: headers,
	})
	if err != nil {
		return err
	}

	var loginResponse LoginResponseResult

	err = resp.DecodeResult(&loginResponse)
	if err != nil {
		return err
	}

	c.token = loginResponse.Token

	return nil
}
