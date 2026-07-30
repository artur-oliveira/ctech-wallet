package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

// m2mWebhookTimeout bounds the outbound notify-back call so a slow/unreachable
// poker endpoint never stalls the confirm/refund request (user-initiated) or
// the reconcile sweep (M2M-retried) for longer than this.
const m2mWebhookTimeout = 5 * time.Second

// HeaderM2MWebhookSignature carries hex(HMAC-SHA256(body, client's secret)) so
// the receiver can authenticate the call — same role as Inter's ?hmac= query
// param on api's own inbound webhook.
const HeaderM2MWebhookSignature = "X-Wallet-Signature"

var m2mWebhookHTTPClient = &http.Client{Timeout: m2mWebhookTimeout}

// m2mWebhookPayload is what RequestingClient's registered URL receives.
// Deliberately NOT trusted by the receiver for crediting anything — same
// "webhook is a wake-up signal, never the source of truth" posture as
// Invariant #11: the receiver must re-GET the purchase by ID to confirm
// before acting on it. This body exists only to prompt that re-query
// promptly, and to carry enough context for the receiver's own logging/UI.
type m2mWebhookPayload struct {
	PurchaseID     string `json:"purchase_id"`
	UserID         string `json:"user_id"`
	SKU            string `json:"sku"`
	Status         string `json:"status"`
	AmountExpected int64  `json:"amount_expected"`
	CreditsGranted int64  `json:"credits_granted"`
}

// dispatchM2MWebhook notifies p.RequestingClient's registered webhook URL that
// p reached a terminal status (confirmed/refunded), then records delivery
// outcome on the purchase row itself so the reconcile sweep
// (RetryFailedM2MWebhooks) can find and retry failures. Never returns an
// error: notify-back failure must never fail or roll back the confirm/refund
// it's reporting on — that transaction already committed. No-op if the
// purchase was never opened by an M2M client, or if that client has no
// registered webhook (logged, since a granted scope with no registration is
// a deploy misconfiguration worth seeing).
func (s *WalletService) dispatchM2MWebhook(ctx context.Context, p *wallet.SandboxPurchase) {
	if p.RequestingClient == "" {
		return
	}
	client, ok := s.m2mClients[p.RequestingClient]
	if !ok || client.WebhookURL == "" {
		slog.ErrorContext(ctx, "m2m webhook: no registered webhook for client", "client", p.RequestingClient, "purchase_id", p.PurchaseID)
		s.markM2MWebhook(ctx, p.PurchaseID, wallet.WebhookFailed)
		return
	}

	body, err := json.Marshal(m2mWebhookPayload{
		PurchaseID: p.PurchaseID, UserID: p.UserID, SKU: p.SKU, Status: p.Status,
		AmountExpected: p.AmountExpected, CreditsGranted: p.CreditsGranted,
	})
	if err != nil {
		slog.ErrorContext(ctx, "m2m webhook: marshal failed", "purchase_id", p.PurchaseID, "err", err)
		s.markM2MWebhook(ctx, p.PurchaseID, wallet.WebhookFailed)
		return
	}

	mac := hmac.New(sha256.New, []byte(client.HMACSecret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	reqCtx, cancel := context.WithTimeout(ctx, m2mWebhookTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, client.WebhookURL, bytes.NewReader(body))
	if err != nil {
		slog.ErrorContext(ctx, "m2m webhook: build request failed", "purchase_id", p.PurchaseID, "err", err)
		s.markM2MWebhook(ctx, p.PurchaseID, wallet.WebhookFailed)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderM2MWebhookSignature, sig)

	resp, err := m2mWebhookHTTPClient.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "m2m webhook: delivery failed, will retry via reconcile sweep", "purchase_id", p.PurchaseID, "client", p.RequestingClient, "err", err)
		s.markM2MWebhook(ctx, p.PurchaseID, wallet.WebhookFailed)
		return
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.WarnContext(ctx, "m2m webhook: non-2xx response, will retry via reconcile sweep", "purchase_id", p.PurchaseID, "client", p.RequestingClient, "status", resp.StatusCode)
		s.markM2MWebhook(ctx, p.PurchaseID, wallet.WebhookFailed)
		return
	}
	s.markM2MWebhook(ctx, p.PurchaseID, wallet.WebhookDelivered)
}

func (s *WalletService) markM2MWebhook(ctx context.Context, purchaseID, status string) {
	if err := s.sandboxPurchases.Update(ctx, purchaseID, map[string]any{"webhook_status": status}); err != nil {
		slog.ErrorContext(ctx, "m2m webhook: failed to record delivery status", "purchase_id", purchaseID, "err", err)
	}
}

// RetryFailedM2MWebhooks re-attempts notify-back for every purchase whose
// last dispatch failed — the reconcile job's counterpart to
// SweepPendingSandboxPurchases, same bounded-batch shape.
func (s *WalletService) RetryFailedM2MWebhooks(ctx context.Context) (retried int, err error) {
	cutoff := time.Now().Add(-sweepAgeThreshold)
	purchases, err := s.sandboxPurchases.ListWebhookFailedOlderThan(ctx, cutoff, reconcileBatch)
	if err != nil {
		return 0, err
	}
	for i := range purchases {
		s.dispatchM2MWebhook(ctx, &purchases[i])
		retried++
	}
	return retried, nil
}
