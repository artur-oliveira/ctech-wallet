package v1

import (
	"context"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/health"
	"gopkg.aoctech.app/wallet/api/internal/awsclient"
	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/middleware"
	"gopkg.aoctech.app/wallet/api/internal/repositories"

	"github.com/gofiber/fiber/v3"
)

// Health check identity.
const (
	healthAPIVersion  = "/v1.0"
	healthServiceID   = "CTech Wallet"
	healthDescription = "Health check details for CTech Wallet API"
)

// RegisterHealth mounts the liveness probe (/v1.0/health) and the detailed health
// check (/v1.0/health-check) from go-common health. nginx/deploy probe the
// dependency-free liveness route; the report is for people and monitoring.
//
// DynamoDB is the only load-bearing dependency: without it the API cannot serve
// a single wallet operation, so it alone can fail the report (503). Cache and
// JWKS degrade to warn (207): the wallet keeps an in-memory cache/lock fallback
// and a warm JWKS cache outlives a brief account outage.
func RegisterHealth(router fiber.Router, clients *awsclient.Clients, cacheBackend cache.Backend, verifier *middleware.Verifier, cfg *config.Config) {
	// A nil dependency must reach go-common as a nil interface/ping (primary: fail,
	// secondary: warn), not as a typed nil or a method value on a nil receiver.
	var db health.TableDescriber
	if clients != nil && clients.DynamoDB != nil {
		db = clients.DynamoDB
	}
	var cachePing, jwksPing func(context.Context) error
	if cacheBackend != nil {
		cachePing = cacheBackend.Ping
	}
	if verifier != nil {
		jwksPing = verifier.Ping
	}
	h := health.New(health.Config{
		ServiceID:   healthServiceID,
		Description: healthDescription,
		Version:     healthAPIVersion,
		ReleaseID:   cfg.AppVersion,
	}).Add(
		health.DynamoDB(db, repositories.TableName(cfg, wallet.TableWallets), true),
		health.Cache(cachePing, false),
		health.JWKS(jwksPing, false),
		health.CPU(),
		health.Memory(),
	)
	router.Get("/health", h.Liveness())
	router.Get("/health-check", h.Report())
}
