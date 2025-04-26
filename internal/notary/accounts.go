package notary

import (
	"bytes"
	"context"
	"encoding/json"
)

type CreateAccountOptions struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type DeleteAccountOptions struct {
	Username string `json:"username"`
}

type Account struct {
	Username string `json:"username"`
}

func (c *Client) ListAccounts() ([]*Account, error) {
	resp, err := c.Requester.Do(context.Background(), &RequestOptions{
		Type:   SyncRequest,
		Method: "GET",
		Path:   "api/v1/accounts",
	})
	if err != nil {
		return nil, err
	}

	var accounts []*Account

	err = resp.DecodeResult(&accounts)
	if err != nil {
		return nil, err
	}

	return accounts, nil
}

func (c *Client) CreateAccount(opts *CreateAccountOptions) error {
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

	_, err = c.Requester.Do(context.Background(), &RequestOptions{
		Type:   SyncRequest,
		Method: "POST",
		Path:   "api/v1/accounts",
		Body:   &body,
	})
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) DeleteAccount(opts *DeleteAccountOptions) error {
	_, err := c.Requester.Do(context.Background(), &RequestOptions{
		Type:   SyncRequest,
		Method: "DELETE",
		Path:   "api/v1/accounts/" + opts.Username,
	})
	if err != nil {
		return err
	}

	return nil
}
