package data

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/pkg/jsonx"
	xhttp "micro-one-api/platform/http"

	"github.com/stretchr/testify/require"
)

type wechatRoundTrip func(*http.Request) (*http.Response, error)

func (f wechatRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type wechatFixture struct {
	config   biz.WechatConfig
	merchant *rsa.PrivateKey
	platform *rsa.PrivateKey
}

func newWechatFixture(t *testing.T) *wechatFixture {
	t.Helper()
	merchant, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	platform, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	directory := t.TempDir()
	privateDER, err := x509.MarshalPKCS8PrivateKey(merchant)
	require.NoError(t, err)
	publicDER, err := x509.MarshalPKIXPublicKey(&platform.PublicKey)
	require.NoError(t, err)
	privatePath, publicPath := filepath.Join(directory, "merchant.pem"), filepath.Join(directory, "platform.pem")
	require.NoError(t, os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0600))
	require.NoError(t, os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), 0600))
	return &wechatFixture{merchant: merchant, platform: platform, config: biz.WechatConfig{
		Enabled: true, AppID: "wx-test-app", MchID: "1900000001", CertificateSerial: "TEST_CERT_SERIAL",
		PrivateKeyPath: privatePath, PublicKeyID: "PUB_KEY_ID_TEST", PublicKeyPath: publicPath,
		APIv3Key: "0123456789abcdef0123456789abcdef", NotifyURL: "https://example.invalid/api/v1/user/payments/wechat/notify",
	}}
}

func wechatTestOrder() *biz.PaymentOrder {
	return &biz.PaymentOrder{TradeNo: "PAY_WECHAT_001", Channel: "wechat", AssetType: biz.PaymentAssetTypeBalance, MoneyCents: 1234, Currency: "CNY", CreatedAt: time.Now()}
}

func (f *wechatFixture) sign(t *testing.T, body, timestamp, nonce string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(timestamp + "\n" + nonce + "\n" + body + "\n"))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.platform, crypto.SHA256, digest[:])
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(signature)
}

func (f *wechatFixture) signedResponse(t *testing.T, code int, body string) *http.Response {
	t.Helper()
	timestamp, nonce := strconv.FormatInt(time.Now().Unix(), 10), "response-nonce"
	header := http.Header{}
	header.Set("Wechatpay-Serial", f.config.PublicKeyID)
	header.Set("Wechatpay-Timestamp", timestamp)
	header.Set("Wechatpay-Nonce", nonce)
	header.Set("Wechatpay-Signature", f.sign(t, body, timestamp, nonce))
	return &http.Response{StatusCode: code, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func (f *wechatFixture) transaction(state string) map[string]any {
	return map[string]any{"appid": f.config.AppID, "mchid": f.config.MchID, "out_trade_no": "PAY_WECHAT_001", "transaction_id": "4200000000001", "trade_type": "NATIVE", "trade_state": state, "amount": map[string]any{"total": int64(1234), "currency": "CNY"}}
}

func wechatJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := jsonx.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func (f *wechatFixture) provider(t *testing.T, roundTrip wechatRoundTrip) biz.WechatPaymentProvider {
	t.Helper()
	provider, err := newWechatPaymentProvider(f.config, roundTrip)
	require.NoError(t, err)
	return provider
}

func TestWechatConfigurationAndNativeCreation(t *testing.T) {
	f := newWechatFixture(t)
	disabled, err := NewWechatPaymentProvider(biz.WechatConfig{})
	require.NoError(t, err)
	require.Nil(t, disabled)
	for _, mutate := range []func(*biz.WechatConfig){
		func(c *biz.WechatConfig) { c.AppID = "" },
		func(c *biz.WechatConfig) { c.APIv3Key = "short" },
		func(c *biz.WechatConfig) { c.NotifyURL = "http://example.invalid/notify" },
		func(c *biz.WechatConfig) { c.NotifyURL += "?secret=value" },
		func(c *biz.WechatConfig) { c.PublicKeyID = "certificate-not-public-key" },
		func(c *biz.WechatConfig) { c.PrivateKeyPath = t.TempDir() },
	} {
		cfg := f.config
		mutate(&cfg)
		_, err := NewWechatPaymentProvider(cfg)
		require.Error(t, err)
	}
	calls := 0
	provider := f.provider(t, func(req *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "https", req.URL.Scheme)
		require.Equal(t, "api.mch.weixin.qq.com", req.URL.Host)
		require.Equal(t, "/v3/pay/transactions/native", req.URL.Path)
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		fields := map[string]string{}
		for _, match := range regexp.MustCompile(`(\w+)="([^"]*)"`).FindAllStringSubmatch(req.Header.Get("Authorization"), -1) {
			fields[match[1]] = match[2]
		}
		require.Equal(t, f.config.MchID, fields["mchid"])
		require.Equal(t, f.config.CertificateSerial, fields["serial_no"])
		digest := sha256.Sum256([]byte(req.Method + "\n" + req.URL.RequestURI() + "\n" + fields["timestamp"] + "\n" + fields["nonce_str"] + "\n" + string(body) + "\n"))
		signature, err := base64.StdEncoding.DecodeString(fields["signature"])
		require.NoError(t, err)
		require.NoError(t, rsa.VerifyPKCS1v15(&f.merchant.PublicKey, crypto.SHA256, digest[:], signature))
		var input struct {
			Amount struct {
				Total    int64  `json:"total"`
				Currency string `json:"currency"`
			} `json:"amount"`
			TimeExpire time.Time `json:"time_expire"`
			NotifyURL  string    `json:"notify_url"`
		}
		require.NoError(t, jsonx.Unmarshal(body, &input))
		require.Equal(t, int64(1234), input.Amount.Total)
		require.Equal(t, "CNY", input.Amount.Currency)
		require.Equal(t, f.config.NotifyURL, input.NotifyURL)
		require.WithinDuration(t, time.Now().Add(wechatOrderTTL), input.TimeExpire, 2*time.Second)
		return f.signedResponse(t, 200, `{"code_url":"weixin://wxpay/bizpayurl/up?pr=test&groupid=00"}`), nil
	})
	result, err := provider.CreateOrder(context.Background(), wechatTestOrder())
	require.NoError(t, err)
	require.Equal(t, "weixin://wxpay/bizpayurl/up?pr=test&groupid=00", result.PayURL)
	require.Equal(t, 1, calls)
	for _, mutate := range []func(*biz.PaymentOrder){
		func(o *biz.PaymentOrder) { o.Currency = "USD" },
		func(o *biz.PaymentOrder) { o.AssetType = biz.PaymentAssetTypeSubscription },
		func(o *biz.PaymentOrder) { o.TradeNo = strings.Repeat("A", 33) },
		func(o *biz.PaymentOrder) { o.MoneyCents = 100000001 },
		func(o *biz.PaymentOrder) { o.CreatedAt = time.Now().Add(-time.Hour) },
	} {
		order := wechatTestOrder()
		mutate(order)
		_, err := provider.CreateOrder(context.Background(), order)
		require.Error(t, err)
	}
	require.Equal(t, 1, calls)
}

func TestWechatNativeURLValidation(t *testing.T) {
	f := newWechatFixture(t)
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{value: "weixin://wxpay/bizpayurl/up?pr=official&groupid=00", valid: true},
		{value: "weixin://wxpay/bizpayurl?pr=alternate", valid: true},
		{value: "https://wxpay/bizpayurl/up?pr=wrong-scheme"},
		{value: "weixin://other/bizpayurl/up?pr=wrong-host"},
		{value: "weixin://wxpay:123/bizpayurl/up?pr=port"},
		{value: "weixin://user@wxpay/bizpayurl/up?pr=userinfo"},
		{value: "weixin://wxpay/bizpayurl/other?pr=wrong-path"},
		{value: "weixin://wxpay/bizpayurl/up?pr=fragment#data"},
		{value: "weixin://wxpay/bizpayurl/up?groupid=00"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			provider := f.provider(t, func(*http.Request) (*http.Response, error) {
				return f.signedResponse(t, 200, wechatJSON(t, map[string]string{"code_url": tc.value})), nil
			})
			result, err := provider.CreateOrder(context.Background(), wechatTestOrder())
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.value, result.PayURL)
		})
	}
}

func TestWechatQueriesOnlyVerifiedMatchingTransactions(t *testing.T) {
	f := newWechatFixture(t)
	for _, tc := range []struct {
		name         string
		mutate       func(map[string]any)
		badSignature bool
		wantError    bool
		paid, closed bool
	}{
		{name: "success", paid: true},
		{name: "pending", mutate: func(tx map[string]any) { tx["trade_state"] = "NOTPAY" }},
		{name: "pending optional fields absent", mutate: func(tx map[string]any) {
			tx["trade_state"] = "NOTPAY"
			delete(tx, "amount")
			delete(tx, "trade_type")
			delete(tx, "transaction_id")
		}},
		{name: "closed", mutate: func(tx map[string]any) { tx["trade_state"] = "CLOSED" }, closed: true},
		{name: "closed optional fields absent", mutate: func(tx map[string]any) {
			tx["trade_state"] = "CLOSED"
			delete(tx, "amount")
			delete(tx, "trade_type")
			delete(tx, "transaction_id")
		}, closed: true},
		{name: "wrong merchant", mutate: func(tx map[string]any) { tx["mchid"] = "other" }, wantError: true},
		{name: "wrong app", mutate: func(tx map[string]any) { tx["appid"] = "other" }, wantError: true},
		{name: "wrong order", mutate: func(tx map[string]any) { tx["out_trade_no"] = "OTHER_ORDER" }, wantError: true},
		{name: "wrong currency", mutate: func(tx map[string]any) { tx["amount"].(map[string]any)["currency"] = "USD" }, wantError: true},
		{name: "wrong amount", mutate: func(tx map[string]any) { tx["amount"].(map[string]any)["total"] = 1 }, wantError: true},
		{name: "missing transaction", mutate: func(tx map[string]any) { delete(tx, "transaction_id") }, wantError: true},
		{name: "success missing amount", mutate: func(tx map[string]any) { delete(tx, "amount") }, wantError: true},
		{name: "success missing currency", mutate: func(tx map[string]any) { delete(tx["amount"].(map[string]any), "currency") }, wantError: true},
		{name: "success missing type", mutate: func(tx map[string]any) { delete(tx, "trade_type") }, wantError: true},
		{name: "pending wrong amount", mutate: func(tx map[string]any) { tx["trade_state"] = "NOTPAY"; tx["amount"].(map[string]any)["total"] = 1 }, wantError: true},
		{name: "pending invalid amount", mutate: func(tx map[string]any) { tx["trade_state"] = "NOTPAY"; tx["amount"].(map[string]any)["total"] = 0 }, wantError: true},
		{name: "closed wrong currency", mutate: func(tx map[string]any) {
			tx["trade_state"] = "CLOSED"
			tx["amount"].(map[string]any)["currency"] = "USD"
		}, wantError: true},
		{name: "pending wrong type", mutate: func(tx map[string]any) { tx["trade_state"] = "NOTPAY"; tx["trade_type"] = "JSAPI" }, wantError: true},
		{name: "unknown status", mutate: func(tx map[string]any) { tx["trade_state"] = "UNKNOWN" }, wantError: true},
		{name: "tampered signature", badSignature: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := f.transaction("SUCCESS")
			if tc.mutate != nil {
				tc.mutate(tx)
			}
			provider := f.provider(t, func(req *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, req.Method)
				require.Equal(t, f.config.MchID, req.URL.Query().Get("mchid"))
				response := f.signedResponse(t, 200, wechatJSON(t, tx))
				if tc.badSignature {
					response.Header.Set("Wechatpay-Signature", "tampered")
				}
				return response, nil
			})
			status, err := provider.QueryOrder(context.Background(), wechatTestOrder())
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.paid, status.Paid)
			require.Equal(t, tc.closed, status.Closed)
		})
	}
}

func TestWechatExpiryCloseRaceAndUncertainResults(t *testing.T) {
	f := newWechatFixture(t)
	for _, finalState := range []string{"CLOSED", "SUCCESS", "NOTPAY"} {
		t.Run("close race "+finalState, func(t *testing.T) {
			methods := []string{}
			provider := f.provider(t, func(req *http.Request) (*http.Response, error) {
				methods = append(methods, req.Method)
				if req.Method == http.MethodPost {
					return nil, errors.New("close timeout")
				}
				state := "NOTPAY"
				if len(methods) == 3 {
					state = finalState
				}
				return f.signedResponse(t, 200, wechatJSON(t, f.transaction(state))), nil
			})
			order := wechatTestOrder()
			order.CreatedAt = time.Now().Add(-time.Hour)
			status, err := provider.QueryOrder(context.Background(), order)
			require.NoError(t, err)
			require.Equal(t, []string{"GET", "POST", "GET"}, methods)
			require.Equal(t, finalState == "CLOSED", status.Closed)
			require.Equal(t, finalState == "SUCCESS", status.Paid)
		})
	}
	for _, tc := range []struct {
		name              string
		signed            bool
		expired           bool
		code              int
		body              string
		wantError, closed bool
	}{
		{name: "signed missing expired", signed: true, expired: true, code: 404, body: `{"code":"ORDER_NOT_EXIST"}`, closed: true},
		{name: "signed missing recent", signed: true, code: 404, body: `{"code":"ORDER_NOT_EXIST"}`},
		{name: "unsigned missing expired", expired: true, code: 404, body: `{"code":"ORDER_NOT_EXIST"}`, wantError: true},
		{name: "unknown error", signed: true, expired: true, code: 500, body: `{"code":"SYSTEM_ERROR"}`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			provider := f.provider(t, func(req *http.Request) (*http.Response, error) {
				calls++
				response := f.signedResponse(t, tc.code, tc.body)
				if !tc.signed {
					response.Header.Del("Wechatpay-Signature")
				}
				return response, nil
			})
			order := wechatTestOrder()
			if tc.expired {
				order.CreatedAt = time.Now().Add(-time.Hour)
			}
			status, err := provider.QueryOrder(context.Background(), order)
			require.Equal(t, 1, calls)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.False(t, status.Paid)
			require.Equal(t, tc.closed, status.Closed)
		})
	}
	provider := f.provider(t, func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
	_, err := provider.CreateOrder(context.Background(), wechatTestOrder())
	require.ErrorContains(t, err, "uncertain")
	_, err = provider.QueryOrder(context.Background(), wechatTestOrder())
	require.Error(t, err)
	provider = f.provider(t, func(*http.Request) (*http.Response, error) {
		return f.signedResponse(t, 200, strings.Repeat("x", xhttp.MaxExternalResponseBody+1)), nil
	})
	_, err = provider.QueryOrder(context.Background(), wechatTestOrder())
	require.Error(t, err)
}

func (f *wechatFixture) webhook(t *testing.T, transaction map[string]any, event string, timestamp time.Time) (map[string]string, []byte) {
	t.Helper()
	block, err := aes.NewCipher([]byte(f.config.APIv3Key))
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce, associated := "nonce-123456", "transaction"
	ciphertext := gcm.Seal(nil, []byte(nonce), []byte(wechatJSON(t, transaction)), []byte(associated))
	body := wechatJSON(t, map[string]any{"id": "event-test", "event_type": event, "resource_type": "encrypt-resource", "resource": map[string]any{"algorithm": "AEAD_AES_256_GCM", "original_type": "transaction", "nonce": nonce, "associated_data": associated, "ciphertext": base64.StdEncoding.EncodeToString(ciphertext)}})
	ts, signatureNonce := fmt.Sprint(timestamp.Unix()), "signature-nonce"
	return map[string]string{"Wechatpay-Serial": f.config.PublicKeyID, "Wechatpay-Timestamp": ts, "Wechatpay-Nonce": signatureNonce, "Wechatpay-Signature": f.sign(t, body, ts, signatureNonce)}, []byte(body)
}

func TestWechatWebhookVerificationAndRejection(t *testing.T) {
	f := newWechatFixture(t)
	provider := f.provider(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("webhook must not call provider")
		return nil, nil
	})
	headers, body := f.webhook(t, f.transaction("SUCCESS"), "TRANSACTION.SUCCESS", time.Now())
	result, err := provider.VerifyWebhook(context.Background(), headers, body)
	require.NoError(t, err)
	require.Equal(t, "PAY_WECHAT_001", result.TradeNo)
	require.Equal(t, "4200000000001", result.ProviderTradeNo)
	require.Equal(t, int64(1234), result.TotalAmount)
	require.Equal(t, "CNY", result.Currency)
	require.True(t, result.Success)
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		event  string
		age    time.Duration
	}{
		{name: "wrong merchant", mutate: func(tx map[string]any) { tx["mchid"] = "other" }},
		{name: "wrong app", mutate: func(tx map[string]any) { tx["appid"] = "other" }},
		{name: "wrong currency", mutate: func(tx map[string]any) { tx["amount"].(map[string]any)["currency"] = "USD" }},
		{name: "no transaction", mutate: func(tx map[string]any) { delete(tx, "transaction_id") }},
		{name: "pending", mutate: func(tx map[string]any) { tx["trade_state"] = "NOTPAY" }},
		{name: "wrong event", event: "TRANSACTION.CLOSED"},
		{name: "expired", age: -6 * time.Minute},
		{name: "future", age: 6 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := f.transaction("SUCCESS")
			if tc.mutate != nil {
				tc.mutate(tx)
			}
			event := tc.event
			if event == "" {
				event = "TRANSACTION.SUCCESS"
			}
			headers, body := f.webhook(t, tx, event, time.Now().Add(tc.age))
			_, err := provider.VerifyWebhook(context.Background(), headers, body)
			require.Error(t, err)
		})
	}
	for _, field := range []string{"Wechatpay-Serial", "Wechatpay-Timestamp", "Wechatpay-Signature", "Wechatpay-Nonce"} {
		headers, body := f.webhook(t, f.transaction("SUCCESS"), "TRANSACTION.SUCCESS", time.Now())
		headers[field] = "tampered"
		_, err := provider.VerifyWebhook(context.Background(), headers, body)
		require.Error(t, err)
	}
	_, err = provider.VerifyWebhook(context.Background(), headers, []byte(`{"resource":{"nonce":"bad"}}`))
	require.Error(t, err)
	var envelope map[string]any
	require.NoError(t, jsonx.Unmarshal(body, &envelope))
	envelope["resource"].(map[string]any)["ciphertext"] = base64.StdEncoding.EncodeToString([]byte("bad GCM content"))
	badBody := wechatJSON(t, envelope)
	headers["Wechatpay-Signature"] = f.sign(t, badBody, headers["Wechatpay-Timestamp"], headers["Wechatpay-Nonce"])
	_, err = provider.VerifyWebhook(context.Background(), headers, []byte(badBody))
	require.Error(t, err)
}

func TestWechatDisabledMerchantDrainsExistingOrders(t *testing.T) {
	f := newWechatFixture(t)
	f.config.Enabled = false
	calls := 0
	provider := f.provider(t, func(req *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, http.MethodGet, req.Method)
		return f.signedResponse(t, 200, wechatJSON(t, f.transaction("SUCCESS"))), nil
	})
	_, err := provider.CreateOrder(context.Background(), wechatTestOrder())
	require.ErrorContains(t, err, "disabled")
	require.Zero(t, calls)
	status, err := provider.QueryOrder(context.Background(), wechatTestOrder())
	require.NoError(t, err)
	require.True(t, status.Paid)
	require.Equal(t, 1, calls)
	headers, body := f.webhook(t, f.transaction("SUCCESS"), "TRANSACTION.SUCCESS", time.Now())
	notification, err := provider.VerifyWebhook(context.Background(), headers, body)
	require.NoError(t, err)
	require.True(t, notification.Success)
	f.config.PrivateKeyPath = ""
	_, err = NewWechatPaymentProvider(f.config)
	require.Error(t, err, "a configured merchant must retain complete keys while draining")
}
