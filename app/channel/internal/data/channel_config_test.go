package data

import (
	"context"
	"testing"

	"micro-one-api/app/channel/internal/biz"

	"github.com/stretchr/testify/require"
)

func TestChannelConfigSurvivesCreateAndUpdate(t *testing.T) {
	repo := setupChannelTestDB(t)
	ctx := context.Background()
	channel := &biz.Channel{Name: "vertex", Status: 1, Group: "default", Models: []string{"gemini"}, Config: biz.ChannelConfig{Region: "us-east1", VertexAIProjectID: "project", APIVersion: "v1"}}
	require.NoError(t, repo.CreateChannel(ctx, channel))
	got, err := repo.FindByID(ctx, channel.ID)
	require.NoError(t, err)
	require.Equal(t, channel.Config, got.Config)
	got.Name = "renamed"
	require.NoError(t, repo.UpdateChannel(ctx, got))
	got, err = repo.FindByID(ctx, channel.ID)
	require.NoError(t, err)
	require.Equal(t, channel.Config, got.Config)
}
