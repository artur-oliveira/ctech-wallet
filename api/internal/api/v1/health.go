package v1

import (
	"bufio"
	"context"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/artur-oliveira/ctech-wallet/api/internal/awsclient"
	"github.com/artur-oliveira/ctech-wallet/api/internal/cache"
	"github.com/artur-oliveira/ctech-wallet/api/internal/config"
	"github.com/artur-oliveira/ctech-wallet/api/internal/domain/wallet"
	"github.com/artur-oliveira/ctech-wallet/api/internal/middleware"
	"github.com/artur-oliveira/ctech-wallet/api/internal/pix"
	"github.com/artur-oliveira/ctech-wallet/api/internal/repositories"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/gofiber/fiber/v3"
)

var startTime = time.Now()

// checkTimeout caps the whole dependency fan-out so a hung dependency can never
// hold the probe open.
const checkTimeout = 2 * time.Second

// Health check statuses (draft-inadarei-api-health-check).
const (
	statusPass = "pass"
	statusWarn = "warn"
	statusFail = "fail"
)

// statusMultiStatus is the HTTP code returned when the API serves traffic with a
// degraded (warn) dependency — the instance must stay in the load balancer.
const statusMultiStatus = 207

// Health check identity.
const (
	healthAPIVersion   = "/v1.0"
	healthServiceID    = "CTech Wallet"
	healthDescription  = "Health check details for CTech Wallet API"
	healthUnavailableV = -1 // observedValue when a check could not be measured
)

// Health check component names.
const (
	componentServer   = "server"
	componentDynamoDB = "dynamodb"
	componentCache    = "cache"
	componentPix      = "pix"
	componentJWKS     = "jwks"
	componentCPU      = "cpu"
	componentMemory   = "memory"
)

// Health check component types and measurements.
const (
	typeSystem        = "system"
	typeDatastoreDB   = "datastore:database"
	typeDatastoreCch  = "datastore:cache"
	typeComponentSvc  = "component:service"
	measureResponse   = "responseTime"
	measureUptime     = "uptime"
	measureUtilizatio = "utilization"
	unitMillisecond   = "ms"
	unitSecond        = "second"
	unitPercent       = "percent"
)

// utilizationWarnPercent is the CPU/memory level above which the instance is
// reported as degraded.
const utilizationWarnPercent = 90

type healthEntry struct {
	ComponentName   string  `json:"componentName"`
	MeasurementName string  `json:"measurementName"`
	ComponentType   string  `json:"componentType"`
	ObservedValue   float64 `json:"observedValue"`
	ObservedUnit    string  `json:"observedUnit"`
	Status          string  `json:"status"`
	Time            string  `json:"time"`
}

type healthResponse struct {
	Status      string                 `json:"status"`
	Version     string                 `json:"version"`
	ReleaseID   string                 `json:"releaseId"`
	ServiceID   string                 `json:"serviceId"`
	Description string                 `json:"description"`
	Checks      map[string]healthEntry `json:"checks"`
}

// liveness is the dependency-free probe: it answers "is the process up", nothing
// more. It carries the running release so a deploy can be verified without an
// authenticated call.
type liveness struct {
	Status    string `json:"status"`
	ReleaseID string `json:"releaseId"`
	ServiceID string `json:"serviceId"`
}

// RegisterHealth mounts the liveness probe (/v1.0/health) and the detailed health
// check (/v1.0/health-check). The ALB target group probes the detailed one and
// accepts 200 and 207, so a degraded (warn) instance keeps serving traffic while
// a 503 takes it out of rotation.
//
// DynamoDB is the only load-bearing dependency: without it the API cannot serve
// a single wallet operation, so it alone can fail the check. Cache, PIX and JWKS
// degrade to warn — the wallet keeps an in-memory cache/lock fallback, PIX is
// only needed on the deposit/withdraw paths, and a warm JWKS cache outlives a
// brief account outage.
func RegisterHealth(router fiber.Router, clients *awsclient.Clients, cacheBackend cache.Backend, pixClient pix.PixClient, verifier *middleware.Verifier, cfg *config.Config) {
	router.Get("/health", func(c fiber.Ctx) error {
		return c.JSON(liveness{Status: statusPass, ReleaseID: cfg.AppVersion, ServiceID: healthServiceID})
	})

	router.Get("/health-check", func(c fiber.Ctx) error {
		nowStr := time.Now().UTC().Format(time.RFC3339Nano)

		ctx, cancel := context.WithTimeout(c.Context(), checkTimeout)
		defer cancel()

		dynamo := checkDynamoDB(ctx, clients.DynamoDB, repositories.TableName(cfg, wallet.TableWallets), nowStr)
		cachec := checkCache(ctx, cacheBackend, nowStr)
		pixc := checkPix(ctx, pixClient, nowStr)
		jwksc := checkJWKS(ctx, verifier, nowStr)
		cpu := checkCPU(nowStr)
		mem := checkMemory(nowStr)

		uptime := healthEntry{
			ComponentName:   componentServer,
			MeasurementName: measureUptime,
			ComponentType:   typeSystem,
			ObservedValue:   time.Since(startTime).Seconds(),
			ObservedUnit:    unitSecond,
			Status:          statusPass,
			Time:            nowStr,
		}

		checks := map[string]healthEntry{
			measureUptime:     uptime,
			componentDynamoDB: dynamo,
			componentCache:    cachec,
			componentPix:      pixc,
			componentJWKS:     jwksc,
			componentCPU:      cpu,
			componentMemory:   mem,
		}

		overall, statusCode := aggregate(checks)
		return c.Status(statusCode).JSON(healthResponse{
			Status:      overall,
			Version:     healthAPIVersion,
			ReleaseID:   cfg.AppVersion,
			ServiceID:   healthServiceID,
			Description: healthDescription,
			Checks:      checks,
		})
	})
}

// aggregate reduces the individual checks to the overall status and HTTP code:
// any fail → 503, else any warn → 207, else 200.
func aggregate(checks map[string]healthEntry) (string, int) {
	overall := statusPass
	for _, e := range checks {
		if e.Status == statusFail {
			return statusFail, fiber.StatusServiceUnavailable
		}
		if e.Status == statusWarn {
			overall = statusWarn
		}
	}
	if overall == statusWarn {
		return statusWarn, statusMultiStatus
	}
	return statusPass, fiber.StatusOK
}

// checkDynamoDB is the only check that can fail the probe — no table, no wallet.
// It describes the wallets table rather than listing tables: DescribeTable is
// resource-scoped (the IAM role has no account-level dynamodb:ListTables) and it
// verifies the load-bearing table actually exists, which ListTables never did.
func checkDynamoDB(ctx context.Context, db *dynamodb.Client, tableName, nowStr string) healthEntry {
	t0 := time.Now()
	_, err := db.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(tableName)})
	ms := float64(time.Since(t0).Milliseconds())
	st := statusPass
	if err != nil {
		st = statusFail
		slog.Error("health check failed", "component", componentDynamoDB, "table", tableName, "error", err)
	}
	return healthEntry{componentDynamoDB, measureResponse, typeDatastoreDB, ms, unitMillisecond, st, nowStr}
}

func checkCache(ctx context.Context, cb cache.Backend, nowStr string) healthEntry {
	if cb == nil {
		return healthEntry{componentCache, measureResponse, typeDatastoreCch, healthUnavailableV, unitMillisecond, statusWarn, nowStr}
	}
	t0 := time.Now()
	err := cb.Ping(ctx)
	ms := float64(time.Since(t0).Milliseconds())
	st := statusPass
	if err != nil {
		st = statusWarn
		slog.Warn("health check degraded", "component", componentCache, "error", err)
	}
	return healthEntry{componentCache, measureResponse, typeDatastoreCch, ms, unitMillisecond, st, nowStr}
}

// checkPix reaches the partner bank through the cached OAuth token — it moves no
// money and never fails the probe.
func checkPix(ctx context.Context, pc pix.PixClient, nowStr string) healthEntry {
	if pc == nil {
		return healthEntry{componentPix, measureResponse, typeComponentSvc, healthUnavailableV, unitMillisecond, statusWarn, nowStr}
	}
	t0 := time.Now()
	err := pc.Ping(ctx)
	ms := float64(time.Since(t0).Milliseconds())
	st := statusPass
	if err != nil {
		st = statusWarn
		slog.Warn("health check degraded", "component", componentPix, "error", err)
	}
	return healthEntry{componentPix, measureResponse, typeComponentSvc, ms, unitMillisecond, st, nowStr}
}

func checkJWKS(ctx context.Context, v *middleware.Verifier, nowStr string) healthEntry {
	if v == nil {
		return healthEntry{componentJWKS, measureResponse, typeComponentSvc, healthUnavailableV, unitMillisecond, statusWarn, nowStr}
	}
	t0 := time.Now()
	err := v.Ping(ctx)
	ms := float64(time.Since(t0).Milliseconds())
	st := statusPass
	if err != nil {
		st = statusWarn
		slog.Warn("health check degraded", "component", componentJWKS, "error", err)
	}
	return healthEntry{componentJWKS, measureResponse, typeComponentSvc, ms, unitMillisecond, st, nowStr}
}

func checkCPU(nowStr string) healthEntry {
	pct := cpuPercent()
	st := statusPass
	if pct < 0 || pct > utilizationWarnPercent {
		st = statusWarn
	}
	return healthEntry{componentCPU, measureUtilizatio, typeSystem, pct, unitPercent, st, nowStr}
}

func checkMemory(nowStr string) healthEntry {
	pct := memoryPercent()
	st := statusPass
	if pct < 0 || pct > utilizationWarnPercent {
		st = statusWarn
	}
	return healthEntry{componentMemory, measureUtilizatio, typeSystem, pct, unitPercent, st, nowStr}
}

func cpuPercent() float64 {
	if runtime.GOOS != "linux" {
		return healthUnavailableV
	}
	f, err := os.Open("/proc/stat")
	if err != nil {
		return healthUnavailableV
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return healthUnavailableV
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return healthUnavailableV
	}
	var vals []int64
	for _, s := range fields[1:] {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			break
		}
		vals = append(vals, v)
	}
	if len(vals) < 4 {
		return healthUnavailableV
	}
	idle := vals[3]
	total := int64(0)
	for _, v := range vals {
		total += v
	}
	if total == 0 {
		return healthUnavailableV
	}
	return roundOne(100.0 * float64(total-idle) / float64(total))
}

func memoryPercent() float64 {
	if runtime.GOOS != "linux" {
		return healthUnavailableV
	}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return healthUnavailableV
	}
	defer func() { _ = f.Close() }()
	info := map[string]int64{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valStr := strings.Fields(strings.TrimSpace(parts[1]))
		if len(valStr) == 0 {
			continue
		}
		v, err := strconv.ParseInt(valStr[0], 10, 64)
		if err == nil {
			info[key] = v
		}
	}
	total, ok1 := info["MemTotal"]
	available, ok2 := info["MemAvailable"]
	if !ok1 || !ok2 || total == 0 {
		return healthUnavailableV
	}
	return roundOne(100.0 * float64(total-available) / float64(total))
}

func roundOne(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
