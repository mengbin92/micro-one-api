package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	v "micro-one-api/api/channel/v1"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestOperatorControlRequestsRemainBoundedAndNoErrorBodyIsLogged(t *testing.T) {
	calls := 0
	c := &ownerClient{base: "https://operator-fixture.invalid", token: "operator-secret", max: 1, client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "Bearer operator-secret", r.Header.Get("Authorization"))
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("operator-secret"))}, nil
	})}}
	var reply v.CreateUpstreamContractReply
	code, err := c.request(context.Background(), "POST", "/api/admin/upstream-contracts", nil, &reply)
	require.NoError(t, err)
	require.Equal(t, 403, code)
	_, err = c.request(context.Background(), "POST", "/api/admin/upstream-contracts", nil, &reply)
	require.ErrorContains(t, err, "budget")
	require.NotContains(t, err.Error(), "operator-secret")
	require.Equal(t, 1, calls)
}
