// Package app wires the wallet API using Fx dependency injection.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/ws"
	apiv1 "gopkg.aoctech.app/wallet/api/internal/api/v1"
	"gopkg.aoctech.app/wallet/api/internal/asaas"
	"gopkg.aoctech.app/wallet/api/internal/awsclient"
	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/lock"
	"gopkg.aoctech.app/wallet/api/internal/pix"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
	"gopkg.aoctech.app/wallet/api/internal/secrets"
	"gopkg.aoctech.app/wallet/api/internal/services"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"go.uber.org/fx"
)

// Module is the root Fx module for the wallet API.
var Module = fx.Options(
	fx.Provide(
		config.Load,
		newAWSClients,
		newDynamoDBClient,
		newCacheBackend,
		newLocker,
		newWsRegistry,
		newLambdaClient,
		newInterTokenManager,
		newLambdaPixClient,
		newKYCClient,
		repositories.NewWalletRepository,
		repositories.NewUserRepository,
		repositories.NewAuditRepository,
		repositories.NewBaasRepository,
		repositories.NewSandboxPurchaseRepository,
		newAsaasSecrets,
		newAsaasClient,
		newBaasService,
		newWalletService,
		newUserService,
		newFiberApp,
	),
	fx.Invoke(registerRoutes),
	fx.Invoke(startServer),
)

func newAWSClients(cfg *config.Config) (*awsclient.Clients, error) {
	return awsclient.New(context.Background(), cfg)
}

func newDynamoDBClient(clients *awsclient.Clients) *dynamodb.Client {
	return clients.DynamoDB
}

func newCacheBackend(lc fx.Lifecycle, cfg *config.Config) (cache.Backend, error) {
	if cfg.RedisURL == "" {
		slog.Warn("VALKEY_URL not set — using in-memory cache/lock (not shared across replicas)")
		return cache.NewMemoryBackend(1000), nil
	}
	rb, err := cache.NewRedisBackend(cfg.RedisURL)
	if err != nil {
		if cfg.Env == "prod" {
			// Fail closed: a memory fallback here would silently drop the
			// fleet-shared per-wallet lock (Invariant #4) — mirror config.go's
			// empty-VALKEY_URL guard instead of degrading.
			return nil, fmt.Errorf("valkey backend init failed in prod: %w", err)
		}
		slog.Warn("redis connection failed, falling back to in-memory", "err", err)
		return cache.NewMemoryBackend(1000), nil
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error { return rb.Ping(ctx) },
		OnStop:  func(context.Context) error { rb.Client().Close(); return nil },
	})
	slog.Info("cache: Redis backend active", "url", cfg.RedisURL)
	return rb, nil
}

func newLocker(c cache.Backend) *lock.Locker {
	return lock.NewLocker(c)
}

// newWsRegistry builds the WebSocket fan-out registry. Reuses the same Redis
// (Valkey) connection as the cache backend when one is configured — falls back
// to an in-memory (single-instance) registry otherwise, exactly like
// newCacheBackend's own Redis/in-memory fallback.
func newWsRegistry(lc fx.Lifecycle, c cache.Backend) ws.Registry {
	rb, ok := c.(*cache.RedisBackend)
	if !ok {
		slog.Warn("ws: no Redis backend — using in-memory registry (not shared across replicas)")
		return ws.NewMemoryRegistry()
	}
	reg := ws.NewRedisRegistry(rb.Client())
	lc.Append(fx.Hook{
		OnStart: reg.Start,
		OnStop:  reg.Stop,
	})
	return reg
}

// newLambdaClient builds the AWS Lambda SDK client used to invoke pix-gateway's
// outbound function.
func newLambdaClient(cfg *config.Config) (*lambda.Client, error) {
	awsCfg, err := awscfg.LoadDefaultConfig(context.Background(), awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	return lambda.NewFromConfig(awsCfg), nil
}

// newInterTokenManager builds the token owner and registers its lifecycle:
// prime on startup (so first traffic never blocks on a fetch) and a background
// refresh loop for the process lifetime.
func newInterTokenManager(lc fx.Lifecycle, client *lambda.Client, cfg *config.Config, locker *lock.Locker, c cache.Backend) *pix.InterTokenManager {
	m := pix.NewInterTokenManager(client, cfg, locker, c)
	var cancel context.CancelFunc
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if _, err := m.Get(ctx, false); err != nil {
				slog.Warn("inter token prime failed (will retry on first use)", "err", err)
			}
			loopCtx, c := context.WithCancel(context.Background())
			cancel = c
			go m.RefreshLoop(loopCtx)
			return nil
		},
		OnStop: func(context.Context) error {
			if cancel != nil {
				cancel()
			}
			return nil
		},
	})
	return m
}

// newLambdaPixClient wraps the Lambda client as api's PixClient implementation.
// api never talks to Inter directly — pix-gateway does. The token manager
// supplies the bearer for every call.
func newLambdaPixClient(client *lambda.Client, cfg *config.Config, tokenMgr *pix.InterTokenManager) pix.PixClient {
	return pix.NewLambdaPixClient(client, cfg.PixGatewayFunctionName, tokenMgr)
}

func newKYCClient(cfg *config.Config) services.KYCClient {
	return kycclient.New(cfg)
}

// asaasSecrets bundles the two SSM SecureString values api fetches once at
// startup and caches for the process lifetime (plan §3.3, §2.3): the AES-256
// master key encrypting every subaccount's Asaas API key at rest, and the
// static token Asaas echoes back on every inbound webhook. Both are the zero
// value when AsaasCustodyEnabled is false — no environment that never touches
// Asaas custody needs these SSM parameters provisioned at all.
type asaasSecrets struct {
	MasterKey    []byte
	WebhookToken string
	ParentAPIKey string
}

func newAsaasSecrets(cfg *config.Config, clients *awsclient.Clients) (*asaasSecrets, error) {
	if !cfg.AsaasCustodyEnabled {
		return &asaasSecrets{}, nil
	}
	store := secrets.NewStore(clients.SSM, cfg.Env)
	ctx := context.Background()
	hexKey, err := store.LoadAsaasMasterKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("asaas master key: %w", err)
	}
	masterKey, err := asaas.MasterKeyFromHex(hexKey)
	if err != nil {
		return nil, err
	}
	token, err := store.LoadAsaasWebhookToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("asaas webhook token: %w", err)
	}
	parentAPIKey, err := store.LoadAsaasParentAPIKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("asaas parent api key: %w", err)
	}
	return &asaasSecrets{MasterKey: masterKey, WebhookToken: token, ParentAPIKey: parentAPIKey}, nil
}

// newAsaasClient wires the real Lambda-backed AsaasClient — api never talks
// to Asaas directly, same posture as Inter (plan §2.2). Reuses the same
// pix-gateway outbound Lambda and *lambda.Client Inter already invokes; only
// the Op discriminator in the wire payload differs.
func newAsaasClient(client *lambda.Client, cfg *config.Config) asaas.AsaasClient {
	return asaas.NewLambdaAsaasClient(client, cfg.PixGatewayFunctionName)
}

func newBaasService(repo *repositories.BaasRepository, walletRepo *repositories.WalletRepository, asaasClient asaas.AsaasClient, audit *repositories.AuditRepository, kyc services.KYCClient, s *asaasSecrets, cfg *config.Config) *services.BaasService {
	return services.NewBaasService(repo, walletRepo, asaasClient, audit, kyc, s.MasterKey, cfg.AsaasParentWalletID, s.ParentAPIKey)
}

func newWalletService(repo *repositories.WalletRepository, users *repositories.UserRepository, audit *repositories.AuditRepository, l *lock.Locker, p pix.PixClient, k services.KYCClient, baas *services.BaasService, sandboxPurchases *repositories.SandboxPurchaseRepository, cfg *config.Config) *services.WalletService {
	svc := services.NewWalletService(repo, users, audit, l, p, k)
	svc.SetBaas(baas)
	svc.SetCustodyEnabled(cfg.AsaasCustodyEnabled)
	svc.SetSandboxPurchases(sandboxPurchases)
	return svc
}

func newUserService(repo *repositories.UserRepository, audit *repositories.AuditRepository) *services.UserService {
	return services.NewUserService(repo, audit)
}

func newFiberApp(cfg *config.Config) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "ctech-wallet-api",
		ReadTimeout:  time.Duration(cfg.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.IdleTimeout) * time.Second,
		ProxyHeader:  fiber.HeaderXForwardedFor,
		TrustProxy:   len(cfg.TrustedProxies) > 0,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Proxies: cfg.TrustedProxies,
		},
		ErrorHandler: errorHandler,
	})
	// AllowCredentials requires explicit origins (a wildcard is rejected by Fiber),
	// so only enable it when origins are configured (production); in dev, allow all.
	corsCfg := cors.Config{
		AllowMethods: []string{"GET", "POST", "OPTIONS"},
		AllowHeaders: []string{"Origin", "Content-Type", "Authorization", "X-Request-ID", apiv1.HeaderIdempotencyKey},
		MaxAge:       3600,
	}
	if len(cfg.CorsAllowedOrigins) > 0 {
		corsCfg.AllowOrigins = cfg.CorsAllowedOrigins
		corsCfg.AllowCredentials = true
	}
	app.Use(cors.New(corsCfg))
	app.Use(requestid.New())
	app.Use(logger.New(logger.Config{
		Format: `{"time":"${time}","status":${status},"latency":"${latency}","method":"${method}","path":"${path}","request-id":"${request-id}"}` + "\n",
	}))
	return app
}

func registerRoutes(app *fiber.App, c cache.Backend, cfg *config.Config, clients *awsclient.Clients, pixClient pix.PixClient, svc *services.WalletService, userSvc *services.UserService, baasSvc *services.BaasService, s *asaasSecrets, wsRegistry ws.Registry) {
	svc.SetBroadcaster(wsRegistry)
	apiv1.Register(app, c, cfg, clients, pixClient, svc, userSvc, baasSvc, s.WebhookToken, wsRegistry)
}

func startServer(lc fx.Lifecycle, app *fiber.App, cfg *config.Config) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			addr := fmt.Sprintf(":%d", cfg.Port)
			slog.Info("starting ctech-wallet-api", "addr", addr, "env", cfg.Env)
			go func() {
				if err := app.Listen(addr); err != nil {
					slog.Error("server error", "err", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			slog.Info("shutting down server")
			return app.ShutdownWithContext(ctx)
		},
	})
}

func errorHandler(c fiber.Ctx, err error) error {
	if f, ok := errors.AsType[*fiber.Error](err); ok {
		return problem.FromFiber(f).Send(c)
	}
	// Never surface a raw error string (DynamoDB/AWS/panic details) to the
	// caller — that is an internal-info leak. Genuine, client-safe failures
	// are returned as *problem.Problem from the handlers and never land here.
	slog.Error("unhandled error", "err", err)
	return problem.InternalServer("erro interno; tente novamente").Send(c)
}
