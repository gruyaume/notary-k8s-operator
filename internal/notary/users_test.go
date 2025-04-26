package notary_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gruyaume/notary-k8s/internal/notary"
)

func TestCreateAccount_Success(t *testing.T) {
	fake := &fakeRequester{
		response: &notary.RequestResponse{
			StatusCode: 200,
			Headers:    http.Header{},
			Result:     []byte(`{"message": "Account created successfully"}`),
		},
		err: nil,
	}
	clientObj := &notary.Client{
		Requester: fake,
	}
	createAccountOpts := &notary.CreateAccountOptions{
		Username: "account@example.com",
		Password: "secret",
	}

	err := clientObj.CreateAccount(createAccountOpts)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestCreateAccount_Failure(t *testing.T) {
	fake := &fakeRequester{
		response: &notary.RequestResponse{
			StatusCode: 400,
			Headers:    http.Header{},
			Result:     []byte(`{"error": "Invalid accountname"}`),
		},
		err: errors.New("requester error"),
	}
	clientObj := &notary.Client{
		Requester: fake,
	}
	createAccountOpts := &notary.CreateAccountOptions{
		Username: "invalid-accountname",
		Password: "secret",
	}

	err := clientObj.CreateAccount(createAccountOpts)
	if err == nil {
		t.Fatalf("expected error, got none")
	}
}

func TestListAccounts_Success(t *testing.T) {
	fake := &fakeRequester{
		response: &notary.RequestResponse{
			StatusCode: 200,
			Headers:    http.Header{},
			Result:     []byte(`[{"imsi": "1234"}, {"imsi": "5678"}]`),
		},
		err: nil,
	}
	clientObj := &notary.Client{
		Requester: fake,
	}

	accounts, err := clientObj.ListAccounts()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(accounts) != 2 {
		t.Fatalf("expected 2 accounts, got: %d", len(accounts))
	}
}

func TestListAccounts_Failure(t *testing.T) {
	fake := &fakeRequester{
		response: &notary.RequestResponse{
			StatusCode: 500,
			Headers:    http.Header{},
			Result:     []byte(`{"error": "Internal server error"}`),
		},
		err: errors.New("requester error"),
	}
	clientObj := &notary.Client{
		Requester: fake,
	}

	accounts, err := clientObj.ListAccounts()
	if err == nil {
		t.Fatalf("expected error, got none")
	}

	if accounts != nil {
		t.Fatalf("expected no accounts, got: %v", accounts)
	}
}

func TestDeleteAccount_Success(t *testing.T) {
	fake := &fakeRequester{
		response: &notary.RequestResponse{
			StatusCode: 200,
			Headers:    http.Header{},
			Result:     []byte(`{"message": "Account deleted successfully"}`),
		},
		err: nil,
	}
	clientObj := &notary.Client{
		Requester: fake,
	}
	deleteAccountOpts := &notary.DeleteAccountOptions{
		Username: "admin@notary.com",
	}

	err := clientObj.DeleteAccount(deleteAccountOpts)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestDeleteAccount_Failure(t *testing.T) {
	fake := &fakeRequester{
		response: &notary.RequestResponse{
			StatusCode: 400,
			Headers:    http.Header{},
			Result:     []byte(`{"error": "Invalid accountname"}`),
		},
		err: errors.New("requester error"),
	}
	clientObj := &notary.Client{
		Requester: fake,
	}
	deleteAccountOpts := &notary.DeleteAccountOptions{
		Username: "invalid-accountname",
	}

	err := clientObj.DeleteAccount(deleteAccountOpts)
	if err == nil {
		t.Fatalf("expected error, got none")
	}
}
