package main

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/upstream/credential"
)

func TestCredentialCoordinationConfiguration(t *testing.T) {
	require.NoError(t, configureCredentialCoordination("", nil, false))
	require.NoError(t, configureCredentialCoordination("single", nil, false))
	require.Error(t, configureCredentialCoordination("redis", nil, true))
	require.Error(t, configureCredentialCoordination("redsi", nil, true))
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	lookup := credential.NewNoopAccountLookup()
	require.Error(t, configureCredentialCoordination("redis", client, false))
	require.NoError(t, configureCredentialCoordination("redis", client, true,
		credential.NewClaudeTokenProvider(lookup), credential.NewOpenAITokenProvider(lookup), credential.NewKimiTokenProvider(lookup)))
	require.Error(t, configureCredentialCoordination("redis", client, true, credential.NewClaudeTokenProvider(nil)))
}
