# Inter Lambda SNS Alerts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Any failure in the two Inter Lambdas (`pix-gateway` outbound and webhook) publishes an e-mail alert through the account's shared SNS topic, the same way `ctech-billing` reports a failed webhook delivery, instead of relying on CloudWatch metrics or alarms.

**Architecture:** `api-commons/alerts` (already in `ctech-go-common`, already a dependency of this module at v1.13.1) provides `alerts.New(ctx, region, topicARN, service, env)` and the `Publisher` interface; an empty topic yields a no-op publisher and publishing never returns an error. A small `internal/alerting` package builds the publisher from the environment (not from `config.Load`, so it still works when config loading is what failed). Both Lambdas alert on cold-start failures and on request-time failures. CDK grants `sns:Publish` on the shared topic and sets `ALERTS_TOPIC_ARN`.

**Tech Stack:** Go (AWS Lambda, `aws-lambda-go`), `gopkg.aoctech.app/api-commons/alerts`, AWS CDK (TypeScript), SNS.

**Spec:** `docs/specs/2026-10-08-wallet-restoration-design.md` (section 5).

## Global Constraints

- Reuse `api-commons/alerts`; do NOT write a second SNS client (DRY across the family: billing already uses it).
- An alert must NEVER carry CPF, payer name, QR/copia-e-cola, tokens, mTLS material or secrets. Allowed content: operation name, `txid`, `withdrawal`/idempotency id, HTTP status, a sanitized error string, Lambda request id.
- Alerting must never fail or slow the main path beyond one bounded SNS call: `Alert` swallows its own errors (library guarantee). Use the Lambda `ctx` so the invocation deadline bounds it.
- Expected business outcomes are NOT failures and are not alerted: Inter returning "PIX key not registered" (`inter.IsKeyNotFound`) on `OpTransfer`, and a `QueryTransfer` / `QueryCharge` that legitimately reports "not found" as a status. Everything else is.
- The shared topic is `ctech-{env}-alerts`, published at SSM `/ctech/{env}/alerts/topic-arn` (ctech-cdk `AlertsStack`). The wallet reads it; it does not create it.
- Never store secrets in the repo. Commit style: Conventional Commits, no emojis, NO attribution trailers. Work on a branch (`feat/inter-lambda-alerts`).

## Review Focus

- Inter retries a failed webhook, so one broken downstream (for example the wallet API being down) can send a burst of identical alerts. The alert text must include the `txid` so the burst is recognisable; consider the mail volume acceptable (SNS e-mail is free at this scale) but do not add retry loops of our own.
- A Lambda that fails during cold start (`config.Load`, SSM read, Inter client init) exits before any handler runs: those exits must alert BEFORE `os.Exit`, or the failure is invisible (no metric, no log line anyone watches).
- `ALERTS_TOPIC_ARN` unset (local run, tests) must silently no-op, not crash.
- The webhook's `401 hmac mismatch` is an attack or a misconfiguration signal but also easy to spam; it alerts at most once per cold start (see Task 3) so a scanner cannot flood the inbox.
- The outbound Lambda returns business errors as `Response.Error`, not as a Lambda invocation error; those must still alert (they are the "any failure" the owner wants), except the allowlisted expected outcomes.

---

### Task 1: Publisher factory and a no-leak test

**Files:**
- Create: `pix-gateway/internal/alerting/alerting.go`
- Test: `pix-gateway/internal/alerting/alerting_test.go`
- Modify: `pix-gateway/internal/config/config.go` (document the env var; the factory reads env directly)

**Interfaces:**
- Consumes: `alerts.New`, `alerts.Nop`, `alerts.Publisher`, `alerts.Alert{Job, Summary, Err, Detail}` from `gopkg.aoctech.app/api-commons/alerts`.
- Produces:
  - `const Service = "pix-gateway"`
  - `func FromEnv(ctx context.Context) alerts.Publisher` (never nil, never errors)
  - `func Failure(ctx context.Context, p alerts.Publisher, job, summary string, err error, detail string)` convenience wrapper

- [ ] **Step 1: Write the failing test**

`pix-gateway/internal/alerting/alerting_test.go`:

```go
package alerting

import (
	"context"
	"testing"

	"gopkg.aoctech.app/api-commons/alerts"
)

func TestFromEnvWithoutTopicIsNop(t *testing.T) {
	t.Setenv("ALERTS_TOPIC_ARN", "")
	p := FromEnv(context.Background())
	if _, ok := p.(alerts.Nop); !ok {
		t.Fatalf("empty topic must yield alerts.Nop, got %T", p)
	}
	// Must not panic or block.
	p.Alert(context.Background(), alerts.Alert{Job: "x", Summary: "y"})
}

type recorder struct{ got []alerts.Alert }

func (r *recorder) Alert(_ context.Context, a alerts.Alert) { r.got = append(r.got, a) }

func TestFailureBuildsAlert(t *testing.T) {
	r := &recorder{}
	Failure(context.Background(), r, "webhook", "confirm-deposit failed", errString("boom"), "txid=abc123")
	if len(r.got) != 1 {
		t.Fatalf("alerts = %d", len(r.got))
	}
	a := r.got[0]
	if a.Job != "webhook" || a.Summary != "confirm-deposit failed" || a.Detail != "txid=abc123" || a.Err == nil {
		t.Fatalf("alert = %+v", a)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd pix-gateway && go test ./internal/alerting/ -v`
Expected: FAIL (package has no non-test files).

- [ ] **Step 3: Implement**

`pix-gateway/internal/alerting/alerting.go`:

```go
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
// operation, status), never payer data, keys or secrets.
func Failure(ctx context.Context, p alerts.Publisher, job, summary string, err error, detail string) {
	p.Alert(ctx, alerts.Alert{Job: job, Summary: summary, Err: err, Detail: detail})
}
```

If `go build` says `alerts` is not at the pinned api-commons version, run `cd pix-gateway && go get gopkg.aoctech.app/api-commons@v1.13.1 && go mod tidy` (the module already requires v1.13.1; the SNS SDK `github.com/aws/aws-sdk-go-v2/service/sns` arrives transitively).

- [ ] **Step 4: Run, commit**

Run: `cd pix-gateway && go build ./... && go test ./internal/alerting/ -v` -> PASS.

```bash
git checkout main && git pull origin main && git checkout -b feat/inter-lambda-alerts
git add pix-gateway && git commit -m "feat(pix-gateway): add SNS alert publisher factory"
```

---

### Task 2: Outbound Lambda alerts

**Files:**
- Modify: `pix-gateway/cmd/outbound/main.go` (`handler` struct, `main`, `handle`)
- Test: `pix-gateway/cmd/outbound/main_test.go` (read the existing file first; extend it)

**Interfaces:**
- Consumes: `alerting.FromEnv`, `alerting.Failure`, `rpc.Request`, `rpc.Response{Error string}`, `rpc.ErrKeyNotFoundSentinel`, `rpc.ErrUnauthorizedSentinel`.
- Produces: `handler` gains `alerts alerts.Publisher`; every `dispatch` that returns `Response.Error != ""`, other than the allowlist, publishes one alert with job `outbound`.

- [ ] **Step 1: Write the failing tests**

Append to `pix-gateway/cmd/outbound/main_test.go`, reusing the fake `inter.PixClient` the file already builds for dispatch tests (read how `handler{pix: fake}` is constructed there):

```go
type alertRecorder struct{ got []alerts.Alert }

func (r *alertRecorder) Alert(_ context.Context, a alerts.Alert) { r.got = append(r.got, a) }

func TestOutboundAlertsOnBankFailure(t *testing.T) {
	rec := &alertRecorder{}
	h := &handler{pix: failingPix{err: errors.New("inter 503")}, alerts: rec}
	resp, err := h.handle(context.Background(), rpc.Request{Op: rpc.OpQueryCharge, Payload: mustJSON(rpc.QueryChargeArgs{Txid: "tx-1"})})
	if err != nil || resp.Error == "" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if len(rec.got) != 1 {
		t.Fatalf("alerts = %d, want 1", len(rec.got))
	}
	if rec.got[0].Job != "outbound" || !strings.Contains(rec.got[0].Detail, "op="+string(rpc.OpQueryCharge)) {
		t.Fatalf("alert = %+v", rec.got[0])
	}
}

func TestOutboundDoesNotAlertOnUnregisteredPixKey(t *testing.T) {
	rec := &alertRecorder{}
	h := &handler{pix: failingPix{err: inter.ErrKeyNotFound}, alerts: rec}
	resp, _ := h.handle(context.Background(), rpc.Request{Op: rpc.OpTransfer, Payload: mustJSON(rpc.TransferArgs{PixKey: "k", Amount: 100, IdemKey: "i"})})
	if resp.Error != rpc.ErrKeyNotFoundSentinel {
		t.Fatalf("resp = %+v", resp)
	}
	if len(rec.got) != 0 {
		t.Fatalf("expected business outcome alerted: %+v", rec.got)
	}
}

func TestOutboundAlertNeverContainsPayload(t *testing.T) {
	rec := &alertRecorder{}
	h := &handler{pix: failingPix{err: errors.New("boom")}, alerts: rec}
	_, _ = h.handle(context.Background(), rpc.Request{Op: rpc.OpTransfer, Payload: mustJSON(rpc.TransferArgs{PixKey: "123.456.789-09", Amount: 100, IdemKey: "i"})})
	for _, a := range rec.got {
		if strings.Contains(a.Detail, "123.456.789-09") || strings.Contains(a.Summary, "123.456.789-09") {
			t.Fatalf("alert leaked the PIX key: %+v", a)
		}
	}
}
```

`failingPix` implements `inter.PixClient` returning `err` from every method; `mustJSON` marshals to `json.RawMessage`. If equivalent helpers already exist in the file, use them.

- [ ] **Step 2: Run to verify they fail**

Run: `cd pix-gateway && go test ./cmd/outbound/ -v` -> FAIL (`handler` has no field `alerts`).

- [ ] **Step 3: Implement**

In `cmd/outbound/main.go`:

```go
type handler struct {
	pix    inter.PixClient
	alerts alerts.Publisher
}
```

(import `gopkg.aoctech.app/api-commons/alerts` and `gopkg.aoctech.app/wallet/pix-gateway/internal/alerting`.)

`main` (alerts built first so even a config failure can report):

```go
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx := context.Background()
	notify := alerting.FromEnv(ctx)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		alerting.Failure(ctx, notify, jobStartup, "outbound Lambda did not start: config load failed", err, "")
		os.Exit(1)
	}
	pixClient, err := newInter(ctx, cfg)
	if err != nil {
		slog.Error("inter client init failed", "err", err)
		alerting.Failure(ctx, notify, jobStartup, "outbound Lambda did not start: Inter client init failed", err, "")
		os.Exit(1)
	}
	h := &handler{pix: pixClient, alerts: notify}
	lambda.Start(h.handle)
}
```

with named constants `const (jobOutbound = "outbound"; jobStartup = "outbound-startup")`. In `handle`, after `resp := h.dispatch(ctx, req)`:

```go
	failed := resp.Error != ""
	slog.InfoContext(ctx, "outbound response", "op", req.Op, "failed", failed)
	if failed && !isExpectedOutcome(resp.Error) {
		alerting.Failure(ctx, h.alerts, jobOutbound, "Inter operation failed",
			errors.New(resp.Error), "op="+string(req.Op))
	}
```

and:

```go
// isExpectedOutcome reports Response.Error values that are normal business
// results, not failures: an unregistered PIX key is a client error the API
// handles by reversing the debit.
func isExpectedOutcome(respError string) bool {
	return respError == rpc.ErrKeyNotFoundSentinel
}
```

`resp.Error` may embed provider text; `errResp` builds it from the Go error. Confirm by reading `errResp` that it does not include request payload fields (PIX key, CPF). If it does, pass only the sentinel/class (`errors.New(classOf(resp.Error))`) instead of the raw text, and keep `TestOutboundAlertNeverContainsPayload` as the guard.

`handler` is also constructed elsewhere in tests; give those literals `alerts: alerts.Nop{}` or leave nil-safe by using `if h.alerts != nil`. Prefer the nil guard inside `alerting.Failure` (`if p == nil { return }`); add it.

- [ ] **Step 4: Run, commit**

Run: `cd pix-gateway && go build ./... && go vet ./... && go test ./... -v 2>&1 | tail -30` -> PASS.

```bash
git add pix-gateway && git commit -m "feat(pix-gateway): alert on outbound Inter failures"
```

---

### Task 3: Webhook Lambda alerts

**Files:**
- Modify: `pix-gateway/cmd/webhook/main.go` (`handler` struct, `main`, `handle`)
- Test: `pix-gateway/cmd/webhook/main_test.go` (read it first; it already fakes `confirmer`)

**Interfaces:**
- Consumes: `alerting.*`, the existing `confirmer` interface and `handler{confirmer, webhookSecret}`.
- Produces: `handler` gains `alerts alerts.Publisher` and `hmacAlerted atomic.Bool`; alerts for: cold-start failures, malformed body (400), confirmation failure (500, with `txid`), and the first hmac mismatch per cold start.

- [ ] **Step 1: Write the failing tests**

```go
func TestWebhookAlertsOnConfirmFailure(t *testing.T) {
	rec := &alertRecorder{}
	h := &handler{confirmer: failingConfirmer{err: errors.New("wallet 502")}, webhookSecret: "s3cret", alerts: rec}
	resp, _ := h.handle(context.Background(), webhookRequest("s3cret", `{"pix":[{"txid":"tx-ABC123"}]}`))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if len(rec.got) != 1 || !strings.Contains(rec.got[0].Detail, "txid=tx-ABC123") {
		t.Fatalf("alerts = %+v", rec.got)
	}
}

func TestWebhookDoesNotAlertOnSuccess(t *testing.T) {
	rec := &alertRecorder{}
	h := &handler{confirmer: okConfirmer{}, webhookSecret: "s3cret", alerts: rec}
	_, _ = h.handle(context.Background(), webhookRequest("s3cret", `{"pix":[{"txid":"tx-1"}]}`))
	if len(rec.got) != 0 {
		t.Fatalf("unexpected alerts: %+v", rec.got)
	}
}

func TestWebhookMalformedBodyAlerts(t *testing.T) {
	rec := &alertRecorder{}
	h := &handler{confirmer: okConfirmer{}, webhookSecret: "s3cret", alerts: rec}
	resp, _ := h.handle(context.Background(), webhookRequest("s3cret", `{not json`))
	if resp.StatusCode != http.StatusBadRequest || len(rec.got) != 1 {
		t.Fatalf("status %d alerts %d", resp.StatusCode, len(rec.got))
	}
}

func TestWebhookHMACMismatchAlertsOncePerColdStart(t *testing.T) {
	rec := &alertRecorder{}
	h := &handler{confirmer: okConfirmer{}, webhookSecret: "s3cret", alerts: rec}
	for range 5 {
		resp, _ := h.handle(context.Background(), webhookRequest("wrong", `{"pix":[{"txid":"tx-1"}]}`))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status %d", resp.StatusCode)
		}
	}
	if len(rec.got) != 1 {
		t.Fatalf("hmac alerts = %d, want exactly 1", len(rec.got))
	}
}

func TestWebhookAlertNeverContainsPayerData(t *testing.T) {
	rec := &alertRecorder{}
	h := &handler{confirmer: failingConfirmer{err: errors.New("wallet 502")}, webhookSecret: "s3cret", alerts: rec}
	body := `{"pix":[{"txid":"tx-9","pagador":{"cpf":"***137303**","nome":"Fulano de Tal"}}]}`
	_, _ = h.handle(context.Background(), webhookRequest("s3cret", body))
	for _, a := range rec.got {
		all := a.Summary + a.Detail + fmt.Sprint(a.Err)
		if strings.Contains(all, "Fulano") || strings.Contains(all, "137303") {
			t.Fatalf("alert leaked payer data: %+v", a)
		}
	}
}
```

Use the file's existing request builder for `webhookRequest` (it must set `QueryStringParameters["hmac"]` and `Body`), and reuse or define `failingConfirmer`/`okConfirmer` against the `confirmer` interface (read it at the top of `cmd/webhook/main.go`). `alertRecorder` is the same 3-line type as in Task 2 (define it in this package's test file; do not export it across packages).

- [ ] **Step 2: Run to verify they fail**

Run: `cd pix-gateway && go test ./cmd/webhook/ -v` -> FAIL.

- [ ] **Step 3: Implement**

```go
type handler struct {
	confirmer     confirmer
	webhookSecret string
	alerts        alerts.Publisher
	// hmacAlerted limits the hmac-mismatch alert to one per cold start so a
	// scanner hitting the endpoint cannot flood the inbox.
	hmacAlerted atomic.Bool
}

const (
	jobWebhook        = "webhook"
	jobWebhookStartup = "webhook-startup"
)
```

`main`: same shape as Task 2 (build `notify := alerting.FromEnv(ctx)` first; on `config.Load`, `newWalletClient`, `loadWebhookSecret` failure call `alerting.Failure(ctx, notify, jobWebhookStartup, "<what failed>", err, "")` BEFORE `os.Exit(1)`); then `h := &handler{confirmer: client, webhookSecret: secret, alerts: notify}`.

`handle`:
- hmac mismatch branch, before returning 401:

```go
		if h.hmacAlerted.CompareAndSwap(false, true) {
			alerting.Failure(ctx, h.alerts, jobWebhook, "webhook rejected: hmac mismatch (possible misconfiguration or probing)", nil, "")
		}
```
- malformed body branch, before returning 400:

```go
		alerting.Failure(ctx, h.alerts, jobWebhook, "webhook payload malformed", err, "")
```
(`err` here is the JSON decode error; confirm its text cannot contain payer data: `encoding/json` syntax errors name offsets and token kinds, not values. If it can echo input, drop `err` and keep the summary only.)
- confirmation failure branch, before returning 500:

```go
			alerting.Failure(ctx, h.alerts, jobWebhook, "webhook confirmation failed; Inter will retry", err, "txid="+p.Txid)
```

Never include `p.Pagador`, `detail.Pagador.*` or the request body in any alert.

- [ ] **Step 4: Run, commit**

Run: `cd pix-gateway && go build ./... && go vet ./... && go test ./... -v 2>&1 | tail -30` -> PASS.

```bash
git add pix-gateway && git commit -m "feat(pix-gateway): alert on webhook failures"
```

---

### Task 4: CDK permissions and environment

**Files:**
- Modify: `cdk/lib/pix-gateway-stack.ts`
- Modify: `cdk/lib/constants.ts` (only if the shared `SSM(env).alerts` constant is not exported by the installed `@aoctech/cdk`)
- Test: `cdk/test/pix-gateway-stack.test.ts` (create, modelled on `cdk/test/reconcile-stack.test.ts`)

**Interfaces:**
- Consumes: SSM parameter `/ctech/{env}/alerts/topic-arn` (created by ctech-cdk `AlertsStack`).
- Produces: both Lambdas get env `ALERTS_TOPIC_ARN` and an IAM statement `sns:Publish` on that topic ARN only.

- [ ] **Step 1: Write the failing test**

`cdk/test/pix-gateway-stack.test.ts`, mirroring how `reconcile-stack.test.ts` instantiates its stack (read it first; copy its `App`/`Template` setup and props):

```ts
import {Template, Match} from 'aws-cdk-lib/assertions';
// build the PixGatewayStack exactly like reconcile-stack.test.ts builds ReconcileStack
// (same env, environment 'dev', a dummy certificateArn, interBaseUrl, interPixKey, walletApiUrl)

test('both Lambdas receive the alerts topic ARN', () => {
  const t = Template.fromStack(stack);
  const fns = t.findResources('AWS::Lambda::Function', {
    Properties: {Environment: {Variables: Match.objectLike({ENVIRONMENT: 'dev'})}},
  });
  const withTopic = Object.values(fns).filter((f: any) => f.Properties.Environment.Variables.ALERTS_TOPIC_ARN !== undefined);
  expect(withTopic).toHaveLength(2);
});

test('both roles may publish to the alerts topic and nothing broader', () => {
  const t = Template.fromStack(stack);
  const policies = t.findResources('AWS::IAM::Policy');
  const publishStatements = Object.values(policies).flatMap((p: any) => p.Properties.PolicyDocument.Statement)
    .filter((s: any) => [].concat(s.Action).includes('sns:Publish'));
  expect(publishStatements).toHaveLength(2);
  for (const s of publishStatements as any[]) {
    expect(s.Resource).not.toBe('*');
  }
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd cdk && npx jest test/pix-gateway-stack.test.ts` -> FAIL.

- [ ] **Step 3: Implement**

In `pix-gateway-stack.ts`, near the other SSM lookups:

```ts
    // The account's shared alert topic (ctech-cdk AlertsStack). Looked up by SSM,
    // never created here: every service publishes its failures to the same one.
    const alertsTopicArn = ssm.StringParameter.valueForStringParameter(this, ALERTS_TOPIC_ARN_PARAM(environment));
```

with, in `constants.ts`, either `import {SSM} from '@aoctech/cdk'` use (`SSM(environment).alerts.topicArn`; check `node_modules/@aoctech/cdk/dist/constants.d.ts`) or, if not exported, a named constant:

```ts
/** SSM path of the account's shared alert topic ARN (ctech-cdk AlertsStack). */
export const ALERTS_TOPIC_ARN_PARAM = (env: Environment) => `/ctech/${env}/alerts/topic-arn`;
```

Add to BOTH Lambda `environment` blocks: `ALERTS_TOPIC_ARN: alertsTopicArn,`. Add to BOTH roles:

```ts
    outboundRole.addToPolicy(new iam.PolicyStatement({
      actions: ['sns:Publish'],
      resources: [alertsTopicArn],
    }));
    webhookRole.addToPolicy(new iam.PolicyStatement({
      actions: ['sns:Publish'],
      resources: [alertsTopicArn],
    }));
```

(`valueForStringParameter` resolves to a CloudFormation parameter reference, which is valid in `resources`. If synth complains about token resolution in a resource ARN, use `cdk.Fn.sub('arn:aws:sns:${AWS::Region}:${AWS::AccountId}:ctech-' + environment + '-alerts')`, the same naming convention billing derives, and keep the SSM value only for the env var.)

- [ ] **Step 4: Verify and commit**

Run: `cd cdk && npm run build && npx jest && npx cdk synth 2>&1 | tail -5` -> PASS, synth succeeds.

```bash
git add cdk && git commit -m "feat(cdk): give the Inter Lambdas the shared alerts topic"
```

---

### Task 5: Docs, runbook and a live smoke test

**Files:**
- Modify: `pix-gateway/CLAUDE.md`, `pix-gateway/README.md`, `OPERATIONS.md`, `cdk/CLAUDE.md`

- [ ] **Step 1:** Document in `pix-gateway/README.md`: the two jobs (`outbound`, `webhook`, plus `*-startup`), what triggers an alert, what never appears in one (no CPF, name, key, QR, secrets), the allowlist (unregistered PIX key), the once-per-cold-start hmac alert, and `ALERTS_TOPIC_ARN`. In `OPERATIONS.md`: "Inter alerts arrive at the address subscribed to `ctech-{env}-alerts`; the subscription must be confirmed by e-mail (a pending subscription looks identical to a working one to the publisher)."
- [ ] **Step 2: Cross-project check** (state in the PR): `ctech-cdk` AlertsStack owns the topic (no change); `ctech-billing` is the reference implementation (no change); the wallet API's own `ALARM` log lines (failed refunds, failed reversals; see `observability.Error`) are NOT covered here and could publish through the same package in a follow-up.
- [ ] **Step 3: Live smoke test after deploy to dev**

```bash
aws sns list-subscriptions-by-topic --topic-arn "$(aws ssm get-parameter --name /ctech/dev/alerts/topic-arn --query Parameter.Value --output text)" \
  --query 'Subscriptions[].[Endpoint,SubscriptionArn]'
```
Expected: the owner's e-mail with a confirmed ARN (not `PendingConfirmation`). Then trigger a webhook failure on purpose: `curl -X POST "https://pix.wallet.dev.aoctech.app/pix/webhook?hmac=wrong"` is rejected by mTLS before reaching the Lambda, so instead invoke the webhook Lambda directly with a bad body:

```bash
aws lambda invoke --function-name <webhook function name from CDK output> \
  --cli-binary-format raw-in-base64-out \
  --payload '{"version":"2.0","queryStringParameters":{"hmac":"<dev secret>"},"body":"{not json"}' /dev/stdout
```
Expected: HTTP 400 in the response and an e-mail with subject `[pix-gateway/dev] webhook`.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "docs: document Inter Lambda SNS alerts and the operator runbook"
```

Suggested PR title: `feat: report Inter Lambda failures through the shared SNS alerts topic`.
