package biz

import "context"

// WechatConfig uses the platform public key mode, without certificate downloads.
type WechatConfig struct {
	Enabled           bool   `json:"enabled"`
	AppID             string `json:"app_id"`
	MchID             string `json:"mch_id"`
	CertificateSerial string `json:"certificate_serial"`
	PrivateKeyPath    string `json:"private_key_path"`
	PublicKeyID       string `json:"public_key_id"`
	PublicKeyPath     string `json:"public_key_path"`
	APIv3Key          string `json:"api_v3_key"`
	NotifyURL         string `json:"notify_url"`
}

type WechatPaymentProvider interface {
	PaymentProvider
	PaymentStatusQuerier
	VerifyWebhook(ctx context.Context, headers map[string]string, body []byte) (*PaymentNotify, error)
}
