// Package app wires the wallet API using Fx dependency injection.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/dataexport"
	"gopkg.aoctech.app/api-commons/deadline"
	"gopkg.aoctech.app/api-commons/erasure"
	"gopkg.aoctech.app/api-commons/oauth2client"
	fiberobs "gopkg.aoctech.app/api-commons/observability/fiber"
	"gopkg.aoctech.app/api-commons/resilient"
	"gopkg.aoctech.app/api-commons/ws"
	apiv1 "gopkg.aoctech.app/wallet/api/internal/api/v1"
	"gopkg.aoctech.app/wallet/api/internal/awsclient"
	"gopkg.aoctech.app/wallet/api/internal/billingclient"
	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/kycclient"
	"gopkg.aoctech.app/wallet/api/internal/lock"
	"gopkg.aoctech.app/wallet/api/internal/problem"
	"gopkg.aoctech.app/wallet/api/internal/rail"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
	"gopkg.aoctech.app/wallet/api/internal/services"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"go.uber.org/fx"
)

// Module is the root Fx module for the wallet API.
var Module = fx.Options(
	fx.Provide(
		config.Load,
		newAWSClients,
		newDynamoDBClient,
		newCacheBackend,
		newRevocationCache,
		newSQSClient,
		newErasureStore,
		newLocker,
		newWsRegistry,
		newKYCClient,
		newBillingClient,
		newRail,
		repositories.NewWalletRepository,
		repositories.NewUserRepository,
		repositories.NewAuditRepository,
		repositories.NewSandboxPurchaseRepository,
		repositories.NewProductPurchaseRepository,
		repositories.NewErasureRepository,
		newWalletService,
		newUserService,
		newErasureService,
		newDataExporter,
		newFiberApp,
	),
	fx.Invoke(registerRoutes),
	fx.Invoke(startErasureConsumer),
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

func newKYCClient(cfg *config.Config, c cache.Backend) services.KYCClient {
	return kycclient.New(cfg, c)
}

func newBillingClient(cfg *config.Config, c cache.Backend) *billingclient.Client {
	return billingclient.New(cfg, c)
}

// newRail is Billing, which owns the Inter connection.
func newRail(bc *billingclient.Client) rail.Rail {
	return rail.NewBilling(bc)
}

func newWalletService(cfg *config.Config, repo *repositories.WalletRepository, users *repositories.UserRepository, audit *repositories.AuditRepository, l *lock.Locker, r rail.Rail, k services.KYCClient, sandboxPurchases *repositories.SandboxPurchaseRepository, productPurchases *repositories.ProductPurchaseRepository, store *erasure.Store, er *repositories.ErasureRepository) *services.WalletService {
	svc := services.NewWalletService(repo, users, audit, l, r, k)
	svc.SetSandboxPurchases(sandboxPurchases)
	svc.SetProductPurchases(productPurchases)
	if store != nil { // never pass a nil *Store through the interface
		svc.SetErasure(store, er)
		svc.SetErased(services.StoreErased(store))
	}
	return svc
}

func newUserService(repo *repositories.UserRepository, audit *repositories.AuditRepository, store *erasure.Store) *services.UserService {
	svc := services.NewUserService(repo, audit)
	if store != nil {
		svc.SetErasureGate(store)
	}
	return svc
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
		AllowMethods:  []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:  []string{"Origin", "Content-Type", "Authorization", "X-Request-ID", apiv1.HeaderIdempotencyKey},
		ExposeHeaders: []string{fiberobs.RequestIDHeader},
		MaxAge:        3600,
	}
	if len(cfg.CorsAllowedOrigins) > 0 {
		corsCfg.AllowOrigins = cfg.CorsAllowedOrigins
		corsCfg.AllowCredentials = true
	}
	app.Use(fiberobs.RequestID())
	app.Use(recover.New(recover.Config{EnableStackTrace: true}))
	app.Use(cors.New(corsCfg))
	app.Use(logger.New(logger.Config{
		Format: `{"time":"${time}","status":${status},"latency":"${latency}","method":"${method}",` +
			`"path":"${path}","request_id":"${respHeader:X-Request-Id}"}` + "\n",
		DisableColors: true,
		Skip: func(c fiber.Ctx) bool {
			return c.Path() == "/v1.0/health" || c.Path() == "/v1.0/health-check"
		},
	}))
	// A time budget for every HTTP request, so a DynamoDB, Valkey, billing, KYC or
	// rail call ends with it instead of outliving the client. Websockets are exempt
	// (the middleware skips an upgrade). Billing's signed invoice.paid is exempt too:
	// it confirms a deposit, and a deposit refund or credit must not be cut by a
	// request budget (Billing retries, and every step is idempotent). Withdrawal
	// reversals run under their own context (services.compensationContext).
	if cfg.RequestTimeout > 0 {
		app.Use(deadline.New(cfg.RequestTimeout, func(c fiber.Ctx) bool {
			return strings.HasSuffix(c.Path(), apiv1.PathBillingEvents)
		}))
	}
	return app
}

func registerRoutes(bc *billingclient.Client, app *fiber.App, c cache.Backend, rev RevocationCache, cfg *config.Config, clients *awsclient.Clients, svc *services.WalletService, userSvc *services.UserService, erasureSvc *services.ErasureService, wsRegistry ws.Registry) {
	svc.SetBroadcaster(wsRegistry)
	var opts []apiv1.Option
	opts = append(opts, apiv1.WithBillingEvents(&apiv1.BillingEvents{Secret: cfg.BillingEventSecret, Invoices: bc, Confirm: svc, Withdrawals: svc}))
	apiv1.Register(app, c, rev, cfg, clients, svc, userSvc, erasureSvc, wsRegistry, opts...)
}

func startServer(lc fx.Lifecycle, app *fiber.App, cfg *config.Config) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			addr := fmt.Sprintf(":%d", cfg.Port)
			slog.Info("starting ctech-wallet-api", "addr", addr, "env", cfg.Env)
			go func() {
				// The ASCII startup banner is written straight to stdout, which is
				// this process's log file. It carries blank lines, and a blank line
				// is not a log record: CloudWatch's PutLogEvents rejects a
				// zero-length message and fails the whole batch it arrived in. The
				// banner tells us nothing slog.Info above does not.
				if err := app.Listen(addr, fiber.ListenConfig{DisableStartupMessage: true}); err != nil {
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
	if errors.Is(err, context.DeadlineExceeded) {
		return problem.GatewayTimeout("a requisição demorou demais; tente novamente").WithCause(err).Send(c)
	}
	if f, ok := errors.AsType[*fiber.Error](err); ok {
		return problem.FromFiber(f).Send(c)
	}
	// Never surface a raw error string (DynamoDB/AWS/panic details) to the
	// caller — that is an internal-info leak. Genuine, client-safe failures
	// are returned as *problem.Problem from the handlers and never land here.
	return problem.InternalServer("erro interno; tente novamente").WithCause(err).Send(c)
}

// RevocationCache is the shared Valkey DB 0 where ctech-account writes the JWT
// revocation list. A distinct type so fx never confuses it with the wallet's
// own cache.Backend (DB 2).
type RevocationCache interface{ cache.Backend }

func newRevocationCache(lc fx.Lifecycle, cfg *config.Config) (RevocationCache, error) {
	if cfg.ValkeyRevocationURL == "" {
		slog.Warn("VALKEY_REVOCATION_URL not set — no token is ever seen as revoked (dev only)")
		return cache.NewMemoryBackend(1), nil
	}
	rb, err := cache.NewRedisBackend(cfg.ValkeyRevocationURL)
	if err != nil {
		return nil, fmt.Errorf("revocation valkey: %w", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { rb.Client().Close(); return nil }})
	return rb, nil
}

func newSQSClient(cfg *config.Config) (*sqs.Client, error) {
	awsCfg, err := awscfg.LoadDefaultConfig(context.Background(), awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	return sqs.NewFromConfig(awsCfg), nil
}

// newErasureStore is nil when the deletion feature is off (ERASURE_QUEUE_URL
// unset): local dev may lack the table, and a store error fails every money
// operation closed.
func newErasureStore(cfg *config.Config, db *dynamodb.Client) *erasure.Store {
	if cfg.ErasureQueueURL == "" {
		return nil
	}
	// No memory of the answer (TTL 0): this gate guards operations that open new
	// exposure with money, and a lock must refuse the very next one, not after the
	// 10 s other services accept.
	return erasure.NewStore(db, cfg.TablePrefix+"_"+wallet.ErasureStateTableSegment, wallet.ErasureTombstoneTTL).WithBlockedMemoryTTL(0)
}

func newErasureService(repo *repositories.WalletRepository, users *repositories.UserRepository, sp *repositories.SandboxPurchaseRepository, pp *repositories.ProductPurchaseRepository, er *repositories.ErasureRepository, k services.KYCClient, charges rail.Rail, cfg *config.Config) *services.ErasureService {
	return services.NewErasureService(repo, users, sp, pp, er, k, cfg.ServiceAudience).WithCharges(charges)
}

// startErasureConsumer runs the account-deletion SQS consumer for the process
// lifetime. Every API instance runs one: the consumer takes one message per
// receive and erasure.Store uses optimistic concurrency, so replicas are safe.
func startErasureConsumer(lc fx.Lifecycle, cfg *config.Config, sqsClient *sqs.Client, store *erasure.Store, svc *services.ErasureService, exporter *services.DataExporter) {
	if store == nil {
		slog.Warn("ERASURE_QUEUE_URL not set — account-deletion lock and purge are OFF (dev only)")
		return
	}
	httpClient := resilient.Client(10*time.Second, resilient.Config{}) // the account: acks and token
	base := strings.TrimRight(cfg.CtechURL, "/")
	acks := erasure.NewAckClient(httpClient, base+wallet.PathAccountErasureAck,
		oauth2client.New(httpClient, nil, base+kycclient.PathToken, cfg.ErasureClientID, cfg.ErasureClientSecret, wallet.ScopeAccountErasureAck))
	// The personal-data export rides the same queue (ctech-account
	// docs/specs/2026-10-17-data-export.md): the part is PUT to a presigned S3 URL
	// and acked, with a longer timeout than the saga's because it is data.
	uploads := &http.Client{Timeout: 60 * time.Second}
	exportAcks := dataexport.NewAckClient(uploads, base+wallet.PathAccountExportAck,
		oauth2client.New(httpClient, nil, base+kycclient.PathToken, cfg.ErasureClientID, cfg.ErasureClientSecret, wallet.ScopeAccountExportAck))
	handler := dataexport.NewHandler(wallet.ErasureServiceID, exporter.Part, uploads, exportAcks)
	consumer := erasure.NewConsumer(sqsClient, cfg.ErasureQueueURL, wallet.ErasureServiceID, store, svc.Purge, acks).
		WithExporter(handler.Handle)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				_ = consumer.Run(ctx)
			}()
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			cancel()
			select {
			case <-done:
			case <-stopCtx.Done():
			}
			return nil
		},
	})
}
