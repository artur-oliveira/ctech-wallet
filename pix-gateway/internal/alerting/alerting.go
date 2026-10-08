// Package alerting builds the publisher the pix-gateway Lambdas use to report
// failures to the account's shared SNS topic (see api-commons/alerts for why
// this is SNS and not CloudWatch). It reads the environment directly instead of
// going through config.Load, so a Lambda whose configuration failed to load can
// still say so.
package alerting

import (
	"context"
	"log/slog"
	"os"

	"gopkg.aoctech.app/api-commons/alerts"
)

// Service names this deployment in every alert subject: "[pix-gateway/prod] job".
const Service = "pix-gateway"

const (
	envAlertsTopicARN = "ALERTS_TOPIC_ARN"
	envRegion         = "AWS_REGION"
	envEnvironment    = "ENVIRONMENT"
	defaultRegion     = "us-east-1"
	defaultEnv        = "dev"
)

// FromEnv returns a Publisher for the configured topic, or a no-op one when
// ALERTS_TOPIC_ARN is empty or the SNS client cannot be built. It never returns
// nil and never fails: refusing to run because alerting is broken would trade a
// silent failure for a certain one.
func FromEnv(ctx context.Context) alerts.Publisher {
	region := os.Getenv(envRegion)
	if region == "" {
		region = defaultRegion
	}
	environment := os.Getenv(envEnvironment)
	if environment == "" {
		environment = defaultEnv
	}
	p, err := alerts.New(ctx, region, os.Getenv(envAlertsTopicARN), Service, environment)
	if err != nil {
		slog.ErrorContext(ctx, "alerting unavailable", "error", err)
		return alerts.Nop{}
	}
	return p
}

// Failure publishes one alert. detail must carry identifiers only (txid,
// operation, status), never payer data, keys or secrets. A nil publisher is a
// no-op so handlers built without one (tests) stay valid.
func Failure(ctx context.Context, p alerts.Publisher, job, summary string, err error, detail string) {
	if p == nil {
		return
	}
	p.Alert(ctx, alerts.Alert{Job: job, Summary: summary, Err: err, Detail: detail})
}
