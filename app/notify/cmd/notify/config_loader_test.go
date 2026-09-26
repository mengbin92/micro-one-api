package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAlertmanagerNotifyTypeConfig(t *testing.T) {
	for _, tt := range []struct {
		name      string
		recipient string
		wantErr   string
	}{
		{name: ""},
		{name: "webhook"},
		{name: "event"},
		{name: "email", recipient: "ops@example.com"},
		{name: "wecom"},
		{name: "dingtalk"},
		{name: "feishu"},
		{name: "slack"},
		{name: "email", wantErr: "alertmanager_email_recipient is required"},
		{name: "unknown", wantErr: "invalid alertmanager_notify_type"},
	} {
		t.Run(tt.name+"/"+tt.recipient, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte("notify_svc:\n  alertmanager_notify_type: "+tt.name+"\n  alertmanager_email_recipient: "+tt.recipient+"\n"), 0600))
			cfg, err := loadConfig(path)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.Nil(t, cfg)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.name, cfg.Bootstrap.NotifySvc.AlertmanagerNotifyType)
			require.Equal(t, tt.recipient, cfg.Bootstrap.NotifySvc.AlertmanagerEmailRecipient)
		})
	}
}
