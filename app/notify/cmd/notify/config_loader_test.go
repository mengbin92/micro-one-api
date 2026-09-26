package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAlertmanagerNotifyTypeConfig(t *testing.T) {
	for _, tt := range []struct {
		name    string
		wantErr bool
	}{
		{name: ""},
		{name: "webhook"},
		{name: "event"},
		{name: "wecom"},
		{name: "dingtalk"},
		{name: "feishu"},
		{name: "slack"},
		{name: "email", wantErr: true},
		{name: "unknown", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte("notify_svc:\n  alertmanager_notify_type: "+tt.name+"\n"), 0600))
			cfg, err := loadConfig(path)
			if tt.wantErr {
				require.ErrorContains(t, err, "invalid alertmanager_notify_type")
				require.Nil(t, cfg)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.name, cfg.Bootstrap.NotifySvc.AlertmanagerNotifyType)
		})
	}
}
