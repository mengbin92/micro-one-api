package data

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/pkg/safefile"
	xhttp "micro-one-api/platform/http"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/validators"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

const wechatOrderTTL = 30 * time.Minute

var wechatTradeNo = regexp.MustCompile(`^[a-zA-Z0-9_\-*]{6,32}$`)

type wechatPaymentProvider struct {
	config biz.WechatConfig
	native *native.NativeApiService
	notify *notify.Handler
}

func NewWechatPaymentProvider(cfg biz.WechatConfig) (biz.WechatPaymentProvider, error) {
	return newWechatPaymentProvider(cfg, http.DefaultTransport.(*http.Transport).Clone())
}

func newWechatPaymentProvider(cfg biz.WechatConfig, transport http.RoundTripper) (biz.WechatPaymentProvider, error) {
	if !cfg.Enabled && cfg.MchID == "" {
		return nil, nil
	}
	notifyURL, err := url.Parse(cfg.NotifyURL)
	if err != nil || notifyURL.Scheme != "https" || notifyURL.Hostname() == "" || notifyURL.User != nil || notifyURL.RawQuery != "" || notifyURL.Fragment != "" {
		return nil, errors.New("wechat notify_url must be an HTTPS URL without credentials, query or fragment")
	}
	if cfg.AppID == "" || cfg.MchID == "" || cfg.CertificateSerial == "" || cfg.PrivateKeyPath == "" || cfg.PublicKeyPath == "" || !strings.HasPrefix(cfg.PublicKeyID, "PUB_KEY_ID_") || len(cfg.APIv3Key) != 32 {
		return nil, errors.New("wechat merchant, key and callback configuration is incomplete")
	}
	privatePEM, err := safefile.ReadFile(cfg.PrivateKeyPath)
	if err != nil {
		return nil, errors.New("wechat merchant private key could not be read")
	}
	privateKey, err := utils.LoadPrivateKey(string(privatePEM))
	if err != nil || privateKey.N.BitLen() < 2048 {
		return nil, errors.New("wechat merchant private key must be RSA with at least 2048 bits")
	}
	publicPEM, err := safefile.ReadFile(cfg.PublicKeyPath)
	if err != nil {
		return nil, errors.New("wechat platform public key could not be read")
	}
	publicKey, err := utils.LoadPublicKey(string(publicPEM))
	if err != nil || publicKey.N.BitLen() < 2048 {
		return nil, errors.New("wechat platform public key must be RSA with at least 2048 bits")
	}
	verifier := verifiers.NewSHA256WithRSAPubkeyVerifier(cfg.PublicKeyID, *publicKey)
	client, err := core.NewClient(context.Background(),
		option.WithWechatPayPublicKeyAuthCipher(cfg.MchID, cfg.CertificateSerial, privateKey, cfg.PublicKeyID, publicKey),
		option.WithHTTPClient(&http.Client{
			Timeout:       10 * time.Second,
			Transport:     wechatBoundedTransport{transport: transport, validator: validators.NewWechatPayResponseValidator(verifier)},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}))
	if err != nil {
		return nil, errors.New("wechat client configuration failed")
	}
	handler, err := notify.NewRSANotifyHandler(cfg.APIv3Key, verifiers.NewSHA256WithRSAPubkeyVerifier(cfg.PublicKeyID, *publicKey))
	if err != nil {
		return nil, errors.New("wechat callback configuration failed")
	}
	return &wechatPaymentProvider{config: cfg, native: &native.NativeApiService{Client: client}, notify: handler}, nil
}

// Bound reads before the SDK's response validator and decoder buffer the body.
type wechatBoundedTransport struct {
	transport http.RoundTripper
	validator *validators.WechatPayResponseValidator
}

func (t wechatBoundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	body, err := xhttp.ReadBody(resp.Body, xhttp.MaxExternalResponseBody)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	// SDK checks API errors before verification; only signed errors may close a missing order.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if err := t.validator.Validate(req.Context(), resp); err != nil {
			return nil, errors.New("wechat API error signature is invalid")
		}
	}
	return resp, nil
}

func validWechatOrder(order *biz.PaymentOrder) bool {
	return order != nil && order.Channel == "wechat" && order.AssetType == biz.PaymentAssetTypeBalance && order.Currency == "CNY" && order.MoneyCents > 0 && order.MoneyCents <= 100000000 && wechatTradeNo.MatchString(order.TradeNo)
}

func (p *wechatPaymentProvider) CreateOrder(ctx context.Context, order *biz.PaymentOrder) (*biz.PaymentProviderOrder, error) {
	if !p.config.Enabled {
		return nil, errors.New("wechat payment creation is disabled")
	}
	if !validWechatOrder(order) || order.CreatedAt.IsZero() {
		return nil, errors.New("wechat Native supports CNY balance orders with a valid merchant order number")
	}
	expires := order.CreatedAt.Add(wechatOrderTTL)
	if !expires.After(time.Now()) {
		return nil, errors.New("wechat payment order has expired")
	}
	response, _, err := p.native.Prepay(ctx, native.PrepayRequest{
		Appid: core.String(p.config.AppID), Mchid: core.String(p.config.MchID),
		Description: core.String("Balance recharge"), OutTradeNo: core.String(order.TradeNo),
		TimeExpire: &expires, NotifyUrl: core.String(p.config.NotifyURL),
		Amount: &native.Amount{Total: core.Int64(order.MoneyCents), Currency: core.String("CNY")},
	})
	if err != nil {
		return nil, errors.New("wechat order creation result is uncertain; query the existing order")
	}
	if response == nil || response.CodeUrl == nil || len(*response.CodeUrl) > 2048 {
		return nil, errors.New("wechat returned an invalid Native payment URL")
	}
	codeURL, err := url.Parse(*response.CodeUrl)
	if err != nil || codeURL.Scheme != "weixin" || codeURL.Host != "wxpay" || codeURL.User != nil || codeURL.Fragment != "" || (codeURL.Path != "/bizpayurl" && codeURL.Path != "/bizpayurl/up") || codeURL.Query().Get("pr") == "" {
		return nil, errors.New("wechat returned an invalid Native payment URL")
	}
	return &biz.PaymentProviderOrder{PayURL: *response.CodeUrl, Payload: `{"provider":"wechat"}`}, nil
}

func (p *wechatPaymentProvider) QueryOrder(ctx context.Context, order *biz.PaymentOrder) (*biz.PaymentProviderStatus, error) {
	if !validWechatOrder(order) {
		return nil, errors.New("invalid wechat payment order")
	}
	status, err := p.query(ctx, order)
	if err != nil {
		return nil, err
	}
	if status.TradeStatus != "NOTPAY" || order.CreatedAt.IsZero() || time.Now().Before(order.CreatedAt.Add(wechatOrderTTL)) {
		return status, nil
	}
	// A payment may win the close race; only a second signed query settles it.
	_, _ = p.native.CloseOrder(ctx, native.CloseOrderRequest{OutTradeNo: core.String(order.TradeNo), Mchid: core.String(p.config.MchID)})
	return p.query(ctx, order)
}

func (p *wechatPaymentProvider) query(ctx context.Context, order *biz.PaymentOrder) (*biz.PaymentProviderStatus, error) {
	transaction, _, err := p.native.QueryOrderByOutTradeNo(ctx, native.QueryOrderByOutTradeNoRequest{OutTradeNo: core.String(order.TradeNo), Mchid: core.String(p.config.MchID)})
	if err != nil {
		var apiError *core.APIError
		if errors.As(err, &apiError) && apiError.Code == "ORDER_NOT_EXIST" {
			return &biz.PaymentProviderStatus{TradeNo: order.TradeNo, TotalAmount: order.MoneyCents, Currency: order.Currency, TradeStatus: "ORDER_NOT_EXIST", Closed: !order.CreatedAt.IsZero() && !time.Now().Before(order.CreatedAt.Add(wechatOrderTTL))}, nil
		}
		return nil, errors.New("wechat payment status could not be verified")
	}
	status, err := p.transactionStatus(transaction)
	if err != nil {
		return nil, err
	}
	if status.TradeNo != order.TradeNo || (status.TotalAmount != 0 && status.TotalAmount != order.MoneyCents) || (status.Currency != "" && status.Currency != order.Currency) {
		return nil, errors.New("wechat payment does not match the local order")
	}
	return status, nil
}

func (p *wechatPaymentProvider) transactionStatus(transaction *payments.Transaction) (*biz.PaymentProviderStatus, error) {
	if transaction == nil || transaction.Appid == nil || *transaction.Appid != p.config.AppID || transaction.Mchid == nil || *transaction.Mchid != p.config.MchID || transaction.OutTradeNo == nil || !wechatTradeNo.MatchString(*transaction.OutTradeNo) || transaction.TradeState == nil || (transaction.TradeType != nil && *transaction.TradeType != "NATIVE") {
		return nil, errors.New("wechat payment identity, amount or currency is invalid")
	}
	status := &biz.PaymentProviderStatus{TradeNo: *transaction.OutTradeNo, TradeStatus: *transaction.TradeState}
	if amount := transaction.Amount; amount != nil {
		if amount.Total != nil {
			if *amount.Total <= 0 {
				return nil, errors.New("wechat payment amount is invalid")
			}
			status.TotalAmount = *amount.Total
		}
		if amount.Currency != nil {
			if *amount.Currency != "CNY" {
				return nil, errors.New("wechat payment currency is invalid")
			}
			status.Currency = *amount.Currency
		}
	}
	if transaction.TransactionId != nil {
		status.ProviderTradeNo = *transaction.TransactionId
	}
	switch status.TradeStatus {
	case "SUCCESS":
		if status.ProviderTradeNo == "" || status.TotalAmount <= 0 || status.Currency != "CNY" || transaction.TradeType == nil {
			return nil, errors.New("wechat successful payment is missing transaction, amount, currency or Native type")
		}
		status.Paid = true
	case "CLOSED", "REVOKED", "PAYERROR":
		status.Closed = true
	case "NOTPAY", "USERPAYING":
	default:
		return nil, errors.New("wechat payment status is unsupported")
	}
	return status, nil
}

func (p *wechatPaymentProvider) VerifyWebhook(ctx context.Context, headers map[string]string, body []byte) (*biz.PaymentNotify, error) {
	if len(body) > xhttp.MaxExternalResponseBody {
		return nil, errors.New("wechat notification is too large")
	}
	var envelope struct {
		Resource struct {
			Nonce string `json:"nonce"`
		} `json:"resource"`
	}
	// The SDK's GCM Open panics on malformed nonce lengths.
	if err := jsonx.Unmarshal(body, &envelope); err != nil || len(envelope.Resource.Nonce) != 12 {
		return nil, errors.New("wechat notification is malformed")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.config.NotifyURL, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("wechat notification request is invalid")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	timestamp, err := strconv.ParseInt(request.Header.Get("Wechatpay-Timestamp"), 10, 64)
	now := time.Now().Unix()
	if err != nil || timestamp <= now-300 || timestamp >= now+300 {
		return nil, errors.New("wechat notification timestamp has expired")
	}
	transaction := new(payments.Transaction)
	notification, err := p.notify.ParseNotifyRequest(ctx, request, transaction)
	if err != nil {
		return nil, errors.New("wechat notification signature or encryption is invalid")
	}
	if notification.EventType != "TRANSACTION.SUCCESS" || notification.ResourceType != "encrypt-resource" || notification.Resource.OriginalType != "transaction" {
		return nil, errors.New("wechat notification event is unsupported")
	}
	status, err := p.transactionStatus(transaction)
	if err != nil || !status.Paid {
		return nil, errors.New("wechat notification is not a valid successful payment")
	}
	return &biz.PaymentNotify{TradeNo: status.TradeNo, ProviderTradeNo: status.ProviderTradeNo, Success: true, Channel: "wechat", TotalAmount: status.TotalAmount, Currency: status.Currency, AppID: p.config.AppID, SellerID: p.config.MchID}, nil
}
