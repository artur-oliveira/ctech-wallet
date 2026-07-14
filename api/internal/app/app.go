// Package app wires the wallet API using Fx dependency injection.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	apiv1 "github.com/artur-oliveira/ctech-wallet/api/internal/api/v1"
	"github.com/artur-oliveira/ctech-wallet/api/internal/awsclient"
	"github.com/artur-oliveira/ctech-wallet/api/internal/cache"
	"github.com/artur-oliveira/ctech-wallet/api/internal/config"
	"github.com/artur-oliveira/ctech-wallet/api/internal/kycclient"
	"github.com/artur-oliveira/ctech-wallet/api/internal/lock"
	"github.com/artur-oliveira/ctech-wallet/api/internal/pix"
	"github.com/artur-oliveira/ctech-wallet/api/internal/problem"
	"github.com/artur-oliveira/ctech-wallet/api/internal/repositories"
	"github.com/artur-oliveira/ctech-wallet/api/internal/services"
	"github.com/artur-oliveira/ctech-wallet/api/internal/ws"

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

func newCacheBackend(lc fx.Lifecycle, cfg *config.Config) cache.Backend {
	if cfg.RedisURL == "" {
		slog.Warn("VALKEY_URL not set — using in-memory cache/lock (not shared across replicas)")
		return cache.NewMemoryBackend(1000)
	}
	rb, err := cache.NewRedisBackend(cfg.RedisURL)
	if err != nil {
		slog.Warn("redis connection failed, falling back to in-memory", "err", err)
		return cache.NewMemoryBackend(1000)
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error { return rb.Ping(ctx) },
		OnStop:  func(context.Context) error { return rb.Client().Close() },
	})
	slog.Info("cache: Redis backend active", "url", cfg.RedisURL)
	return rb
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
func newInterTokenManager(lc fx.Lifecycle, client *lambda.Client, cfg *config.Config, locker *lock.Locker) *pix.InterTokenManager {
	m := pix.NewInterTokenManager(client, cfg, locker)
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

func newWalletService(repo *repositories.WalletRepository, users *repositories.UserRepository, audit *repositories.AuditRepository, l *lock.Locker, p pix.PixClient, k services.KYCClient) *services.WalletService {
	return services.NewWalletService(repo, users, audit, l, p, k)
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

func registerRoutes(app *fiber.App, c cache.Backend, cfg *config.Config, clients *awsclient.Clients, pixClient pix.PixClient, svc *services.WalletService, userSvc *services.UserService, wsRegistry ws.Registry) {
	svc.SetBroadcaster(wsRegistry)
	apiv1.Register(app, c, cfg, clients, pixClient, svc, userSvc, wsRegistry)
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
	return problem.InternalServer(err.Error()).Send(c)
}
