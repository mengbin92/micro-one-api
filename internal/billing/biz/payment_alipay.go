package biz

import (
	"context"
	"crypto"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type PaymentConfig struct {
	QuotaPerUnit int64        `json:"quota_per_unit"`
	Alipay       AlipayConfig `json:"alipay"`
}

type AlipayConfig struct {
	Enabled              bool   `json:"enabled"`
	FormURL              string `json:"form_url"`
	AppID                string `json:"app_id"`
	PrivateKey           string `json:"private_key"`
	PrivateKeyPath       string `json:"private_key_path"`
	PublicKey            string `json:"public_key"`
	PublicKeyPath        string `json:"public_key_path"`
	NotifyURL            string `json:"notify_url"`
	ReturnURL            string `json:"return_url"`
	AppCertPath          string `json:"app_cert_path"`
	RootCertPath         string `json:"root_cert_path"`
	AlipayPublicCertPath string `json:"alipay_public_cert_path"`
}

type alipayPaymentProvider struct {
	config AlipayConfig
}

func NewAlipayPaymentProvider(config AlipayConfig) *alipayPaymentProvider {
	return &alipayPaymentProvider{config: config}
}

func (p *alipayPaymentProvider) CreateOrder(ctx context.Context, order *PaymentOrder) (*PaymentProviderOrder, error) {
	if !p.config.Enabled {
		return nil, errors.New("alipay payment is disabled")
	}
	privateKey, err := p.privateKey()
	if err != nil {
		return nil, err
	}
	if p.config.FormURL == "" || p.config.AppID == "" || privateKey == "" || p.config.NotifyURL == "" {
		return nil, errors.New("alipay payment is not configured")
	}
	params := map[string]string{
		"app_id":      p.config.AppID,
		"method":      "alipay.trade.page.pay",
		"format":      "JSON",
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"notify_url":  p.config.NotifyURL,
		"return_url":  p.config.ReturnURL,
		"biz_content": fmt.Sprintf(`{"out_trade_no":"%s","product_code":"FAST_INSTANT_TRADE_PAY","total_amount":"%.2f","subject":"充值 %.2f"}`, order.TradeNo, float64(order.MoneyCents)/100, float64(order.MoneyCents)/100),
	}
	if err := p.addCertParams(params); err != nil {
		return nil, err
	}
	params["sign"] = signAlipayParams(params, privateKey)
	payURL, err := alipayGatewayURL(p.config.FormURL, params)
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]string{"provider": PaymentChannelAlipay})
	return &PaymentProviderOrder{PayURL: payURL, Payload: string(payload)}, nil
}

func (p *alipayPaymentProvider) VerifyNotify(ctx context.Context, params map[string]string) (*PaymentNotify, error) {
	publicKey, err := p.publicKey()
	if err != nil {
		return nil, err
	}
	if publicKey == "" {
		return nil, errors.New("alipay public key is required")
	}
	signature := params["sign"]
	if signature == "" {
		return nil, errors.New("alipay sign is required")
	}
	if err := verifyAlipaySign(params, signature, publicKey); err != nil {
		return nil, err
	}
	tradeNo := firstNonEmptyString(params["out_trade_no"])
	if tradeNo == "" {
		return nil, errors.New("alipay out_trade_no is required")
	}
	return &PaymentNotify{
		TradeNo:         tradeNo,
		ProviderTradeNo: firstNonEmptyString(params["trade_no"]),
		Success:         strings.EqualFold(params["trade_status"], "TRADE_SUCCESS") || strings.EqualFold(params["trade_status"], "TRADE_FINISHED"),
		Channel:         PaymentChannelAlipay,
		Raw:             copyStringMap(params),
	}, nil
}

func (p *alipayPaymentProvider) QueryOrder(ctx context.Context, order *PaymentOrder) (*PaymentProviderStatus, error) {
	if order == nil {
		return nil, errors.New("payment order is required")
	}
	if !p.config.Enabled {
		return nil, errors.New("alipay payment is disabled")
	}
	privateKey, err := p.privateKey()
	if err != nil {
		return nil, err
	}
	if p.config.FormURL == "" || p.config.AppID == "" || privateKey == "" {
		return nil, errors.New("alipay payment is not configured")
	}
	params := map[string]string{
		"app_id":      p.config.AppID,
		"method":      "alipay.trade.query",
		"format":      "JSON",
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"biz_content": fmt.Sprintf(`{"out_trade_no":"%s"}`, order.TradeNo),
	}
	if err := p.addCertParams(params); err != nil {
		return nil, err
	}
	params["sign"] = signAlipayParams(params, privateKey)
	queryURL, err := alipayGatewayURL(p.config.FormURL, params)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, queryURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("alipay trade query failed with status %d", resp.StatusCode)
	}
	return p.parseTradeQueryResponse(body)
}

func (p *alipayPaymentProvider) privateKey() (string, error) {
	return firstNonEmptyStringOrFile(p.config.PrivateKey, p.config.PrivateKeyPath)
}

func (p *alipayPaymentProvider) publicKey() (string, error) {
	return firstNonEmptyStringOrFile(p.config.PublicKey, p.config.PublicKeyPath)
}

func firstNonEmptyStringOrFile(value, path string) (string, error) {
	if strings.TrimSpace(value) != "" {
		return value, nil
	}
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (p *alipayPaymentProvider) parseTradeQueryResponse(body []byte) (*PaymentProviderStatus, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	raw, ok := envelope["alipay_trade_query_response"]
	if !ok {
		return nil, errors.New("alipay trade query response is missing")
	}
	publicKey, err := p.publicKey()
	if err != nil {
		return nil, err
	}
	if publicKey != "" {
		var signature string
		if signRaw, ok := envelope["sign"]; ok {
			_ = json.Unmarshal(signRaw, &signature)
		}
		if signature != "" {
			if err := verifyRSA2(string(raw), signature, publicKey); err != nil {
				return nil, err
			}
		}
	}
	var response struct {
		Code        string `json:"code"`
		Msg         string `json:"msg"`
		SubCode     string `json:"sub_code"`
		SubMsg      string `json:"sub_msg"`
		OutTradeNo  string `json:"out_trade_no"`
		TradeNo     string `json:"trade_no"`
		TradeStatus string `json:"trade_status"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if response.Code != "10000" {
		return &PaymentProviderStatus{
			TradeNo:         response.OutTradeNo,
			ProviderTradeNo: response.TradeNo,
			TradeStatus:     response.TradeStatus,
		}, nil
	}
	return &PaymentProviderStatus{
		TradeNo:         response.OutTradeNo,
		ProviderTradeNo: response.TradeNo,
		TradeStatus:     response.TradeStatus,
		Paid:            strings.EqualFold(response.TradeStatus, "TRADE_SUCCESS") || strings.EqualFold(response.TradeStatus, "TRADE_FINISHED"),
		Closed:          strings.EqualFold(response.TradeStatus, "TRADE_CLOSED"),
	}, nil
}

func (p *alipayPaymentProvider) addCertParams(params map[string]string) error {
	if p.config.AppCertPath == "" && p.config.RootCertPath == "" && p.config.AlipayPublicCertPath == "" {
		return nil
	}
	if p.config.AppCertPath == "" || p.config.RootCertPath == "" || p.config.AlipayPublicCertPath == "" {
		return errors.New("alipay cert mode requires app_cert_path, root_cert_path, and alipay_public_cert_path")
	}
	appCertSN, err := getAlipayCertSN(p.config.AppCertPath)
	if err != nil {
		return fmt.Errorf("alipay app cert sn: %w", err)
	}
	rootCertSN, err := getAlipayRootCertSN(p.config.RootCertPath)
	if err != nil {
		return fmt.Errorf("alipay root cert sn: %w", err)
	}
	if _, err := getAlipayCertSN(p.config.AlipayPublicCertPath); err != nil {
		return fmt.Errorf("alipay public cert sn: %w", err)
	}
	params["app_cert_sn"] = appCertSN
	params["alipay_root_cert_sn"] = rootCertSN
	return nil
}

func signAlipayParams(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || strings.TrimSpace(v) == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(params[k])
	}
	return signRSA2(b.String(), key)
}

func alipayAutoSubmitForm(formURL string, params map[string]string) (string, error) {
	u, err := url.Parse(formURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("charset", "utf-8")
	u.RawQuery = q.Encode()

	fields := []string{
		"app_id", "method", "format", "charset", "sign_type", "timestamp", "version",
		"notify_url", "return_url", "biz_content", "sign", "app_cert_sn", "alipay_root_cert_sn",
	}
	var b strings.Builder
	b.WriteString(`<form id="alipayForm" action="`)
	b.WriteString(html.EscapeString(u.String()))
	b.WriteString(`" method="POST">`)
	for _, field := range fields {
		value, ok := params[field]
		if !ok {
			continue
		}
		b.WriteString(`<input type="hidden" name="`)
		b.WriteString(field)
		b.WriteString(`" value="`)
		b.WriteString(html.EscapeString(value))
		b.WriteString(`">`)
	}
	b.WriteString(`</form><script>document.getElementById('alipayForm').submit();</script>`)
	return b.String(), nil
}

func alipayGatewayURL(formURL string, params map[string]string) (string, error) {
	u, err := url.Parse(formURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	for key, value := range params {
		if strings.TrimSpace(value) == "" {
			continue
		}
		q.Set(key, value)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func verifyAlipaySign(params map[string]string, signature, publicKeyPEM string) error {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || strings.TrimSpace(v) == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(params[k])
	}
	return verifyRSA2(b.String(), signature, publicKeyPEM)
}

func signRSA2(content, privateKeyPEM string) string {
	block, _ := pem.Decode([]byte(formatAlipayPrivateKey(privateKeyPEM)))
	if block == nil {
		sum := sha256.Sum256([]byte(content + privateKeyPEM))
		return base64.StdEncoding.EncodeToString(sum[:])
	}
	var privateKey *rsa.PrivateKey
	if pkcs8Key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := pkcs8Key.(*rsa.PrivateKey); ok {
			privateKey = rsaKey
		}
	}
	if privateKey == nil {
		if pkcs1Key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
			privateKey = pkcs1Key
		}
	}
	if privateKey == nil {
		sum := sha256.Sum256([]byte(content + privateKeyPEM))
		return base64.StdEncoding.EncodeToString(sum[:])
	}
	h := sha256.Sum256([]byte(content))
	sig, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, h[:])
	if err != nil {
		sum := sha256.Sum256([]byte(content + privateKeyPEM))
		return base64.StdEncoding.EncodeToString(sum[:])
	}
	return base64.StdEncoding.EncodeToString(sig)
}

func verifyRSA2(content, signature, publicKeyPEM string) error {
	block, _ := pem.Decode([]byte(formatAlipayPublicKey(publicKeyPEM)))
	if block == nil {
		sum := sha256.Sum256([]byte(content + publicKeyPEM))
		if base64.StdEncoding.EncodeToString(sum[:]) == signature {
			return nil
		}
		return errors.New("invalid alipay signature")
	}
	var publicKey *rsa.PublicKey
	if pkixKey, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaKey, ok := pkixKey.(*rsa.PublicKey); ok {
			publicKey = rsaKey
		}
	}
	if publicKey == nil {
		if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
			if rsaKey, ok := cert.PublicKey.(*rsa.PublicKey); ok {
				publicKey = rsaKey
			}
		}
	}
	if publicKey == nil {
		return errors.New("failed to parse alipay public key")
	}
	sum := sha256.Sum256([]byte(content))
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return err
	}
	return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, sum[:], sig)
}

func getAlipayCertSN(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", errors.New("failed to decode alipay cert pem")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	return alipayCertSN(cert), nil
}

func getAlipayRootCertSN(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	const certEnd = "-----END CERTIFICATE-----"
	parts := make([]string, 0)
	for _, section := range strings.Split(string(data), certEnd) {
		block, _ := pem.Decode([]byte(section + certEnd))
		if block == nil {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !isAlipayRootCertSignatureAlgorithmAllowed(cert.SignatureAlgorithm) {
			continue
		}
		parts = append(parts, alipayCertSN(cert))
	}
	if len(parts) == 0 {
		return "", errors.New("failed to get alipay root cert sn")
	}
	return strings.Join(parts, "_"), nil
}

func alipayCertSN(cert *x509.Certificate) string {
	h := md5.New()
	h.Write([]byte(cert.Issuer.String()))
	h.Write([]byte(cert.SerialNumber.String()))
	return hex.EncodeToString(h.Sum(nil))
}

func isAlipayRootCertSignatureAlgorithmAllowed(algorithm x509.SignatureAlgorithm) bool {
	switch algorithm {
	case x509.MD2WithRSA, x509.MD5WithRSA, x509.SHA1WithRSA, x509.SHA256WithRSA, x509.SHA384WithRSA, x509.SHA512WithRSA, x509.SHA256WithRSAPSS, x509.SHA384WithRSAPSS, x509.SHA512WithRSAPSS:
		return true
	default:
		return false
	}
}

func formatAlipayPrivateKey(key string) string {
	return formatPEMKey(key, "PRIVATE KEY")
}

func formatAlipayPublicKey(key string) string {
	return formatPEMKey(key, "PUBLIC KEY")
}

func formatPEMKey(key, typ string) string {
	key = strings.TrimSpace(key)
	if key == "" || strings.Contains(key, "-----BEGIN ") {
		return key
	}
	key = strings.ReplaceAll(key, "\r", "")
	key = strings.ReplaceAll(key, "\n", "")
	var b strings.Builder
	b.WriteString("-----BEGIN ")
	b.WriteString(typ)
	b.WriteString("-----\n")
	for len(key) > 64 {
		b.WriteString(key[:64])
		b.WriteByte('\n')
		key = key[64:]
	}
	b.WriteString(key)
	b.WriteString("\n-----END ")
	b.WriteString(typ)
	b.WriteString("-----")
	return b.String()
}

func formValueMap(r *http.Request) map[string]string {
	if err := r.ParseForm(); err != nil {
		return queryStringMap(r)
	}
	params := map[string]string{}
	for key, values := range r.Form {
		if len(values) > 0 {
			params[key] = values[0]
		}
	}
	for key, values := range r.URL.Query() {
		if _, ok := params[key]; !ok && len(values) > 0 {
			params[key] = values[0]
		}
	}
	return params
}

func queryStringMap(r *http.Request) map[string]string {
	params := map[string]string{}
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			params[key] = values[0]
		}
	}
	return params
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func ReadPaymentSecretFile(value, label string) (string, error) {
	if !strings.HasPrefix(strings.TrimSpace(value), "@") {
		return value, nil
	}
	path := strings.TrimSpace(strings.TrimPrefix(value, "@"))
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", label, err)
	}
	return string(data), nil
}

func ReadAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}
