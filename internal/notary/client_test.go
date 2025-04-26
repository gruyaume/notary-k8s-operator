package notary_test

import (
	"context"

	"github.com/gruyaume/notary-k8s/internal/notary"
)

type fakeRequester struct {
	response *notary.RequestResponse
	err      error
	// lastOpts holds the most recent RequestOptions passed in, so that we can verify
	// that the Login method constructs the request correctly.
	lastOpts *notary.RequestOptions
}

func (f *fakeRequester) Do(ctx context.Context, opts *notary.RequestOptions) (*notary.RequestResponse, error) {
	f.lastOpts = opts
	return f.response, f.err
}
