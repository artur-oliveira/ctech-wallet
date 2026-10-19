package services

import (
	"context"
	"log/slog"
	"time"

	"gopkg.aoctech.app/api-commons/alerts"
)

// alertTimeout bounds the SNS publish so a slow topic cannot hold a money flow that already failed.
const alertTimeout = 3 * time.Second

// Alert job names (the mail subject carries them).
const (
	alertJobDepositRefund    = "deposit-refund-failed"
	alertJobExcessRefund     = "excess-payment-refund-failed"
	alertJobWithdrawReversal = "withdrawal-reversal-failed"
)

// SetAlerts wires the operator alert publisher; unset means alerts.Nop. The ALARM log line stays
// either way: the alert is a second channel, never a replacement.
func (s *WalletService) SetAlerts(p alerts.Publisher) {
	if p != nil {
		s.alerts = p
	}
}

// alarm tells a human that money needs manual attention. It never fails the caller and is not
// cancelled with the request (the 8 s budget may already be spent when a refund fails).
func (s *WalletService) alarm(ctx context.Context, job, summary string, err error, detail string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), alertTimeout)
	defer cancel()
	s.alerts.Alert(ctx, alerts.Alert{Job: job, Summary: summary, Err: err, Detail: detail})
}

// NewAlerts builds the publisher from config; any failure degrades to Nop (logged), never to a startup error.
func NewAlerts(ctx context.Context, region, topicARN, env string) alerts.Publisher {
	p, err := alerts.New(ctx, region, topicARN, "wallet", env)
	if err != nil {
		slog.Warn("alerts: publisher unavailable, operator alerts are log-only", "err", err)
		return alerts.Nop{}
	}
	return p
}
