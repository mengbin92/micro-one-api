package service

import (
	"context"
	"io"
	"net/http"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/authorization"
)

func (s *BillingService) SetWechatVerifier(verifier biz.WechatPaymentProvider) {
	s.wechatVerifier = verifier
}

func (s *BillingService) ListPaymentChannels(ctx context.Context, _ *billingv1.ListPaymentChannelsRequest) (*billingv1.ListPaymentChannelsResponse, error) {
	if _, err := authorization.PrepareSelf(ctx, s.ownerAuth, "billing.self", "billing.payment.read"); err != nil {
		return nil, err
	}
	reply := &billingv1.ListPaymentChannelsResponse{}
	if s.paymentUc == nil {
		return reply, nil
	}
	for _, c := range s.paymentUc.ListPaymentChannels() {
		reply.Channels = append(reply.Channels, &billingv1.PaymentChannel{Channel: c.Channel, DisplayName: c.DisplayName, Currency: c.Currency, AssetTypes: c.AssetTypes})
	}
	return reply, nil
}

func (s *BillingService) HandleWechatNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.wechatVerifier == nil || s.paymentUc == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	// Preserve the signed bytes and bound input before signature verification/decryption.
	const limit = 64 << 10
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || len(body) > limit {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	headers := map[string]string{}
	for _, name := range []string{"Wechatpay-Timestamp", "Wechatpay-Nonce", "Wechatpay-Serial", "Wechatpay-Signature", "Wechatpay-Signature-Type"} {
		headers[name] = r.Header.Get(name)
	}
	notify, err := s.wechatVerifier.VerifyWebhook(r.Context(), headers, body)
	if err != nil || notify == nil || !notify.Success {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if _, err := s.paymentUc.AcceptVerifiedNotify(r.Context(), notify); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
