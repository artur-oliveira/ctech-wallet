package services

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

// ExportSource is what DataExporter reads. The repositories satisfy it through
// ExportSources.
type ExportSource interface {
	Wallets(ctx context.Context, userID string) ([]wallet.Wallet, error)
	Statement(ctx context.Context, walletID string, limit int, startKey map[string]types.AttributeValue) (*repositories.QueryResult, error)
	Deposits(ctx context.Context, userID string) ([]wallet.PixDeposit, error)
	Withdrawals(ctx context.Context, userID string) ([]wallet.Withdrawal, error)
	Holds(ctx context.Context, userID string) ([]wallet.Hold, error)
	SandboxPurchases(ctx context.Context, userID string, limit int, start map[string]types.AttributeValue) (*repositories.Page[wallet.SandboxPurchase], error)
	ProductPurchases(ctx context.Context, userID string, limit int, start map[string]types.AttributeValue) (*repositories.Page[wallet.ProductPurchase], error)
	User(ctx context.Context, userID string) (*wallet.User, error)
	Audit(ctx context.Context, userID string, limit int) ([]wallet.AuditEvent, error)
}

// exportMaxItems bounds one file; ledger and purchase reads stop paging at it.
// auditExportLimit is the most recent audit events (the repository reads one page).
// ponytail: 100k entries covers any wallet; stream to S3 in pages if one outgrows memory.
const (
	exportMaxItems   = 100_000
	exportPageSize   = 100
	auditExportLimit = 1000
)

// DataExporter builds the wallet's part of the personal-data export (LGPD art.
// 18, V; ctech-account docs/specs/2026-10-17-data-export.md): the person's
// wallets, their whole statement, deposits, withdrawals, holds, purchases,
// responsible-gambling settings and audit trail. It only reads; the ledger is
// append-only and the export is not an edit (Invariant #2).
//
// Counterparties are opaque (E4): a ledger line carries the other side only as a
// wallet id or the caller's reference. The one third party that could appear is the
// payer of a deposit the CPF check rejected, and that payer's name and CPF are
// left out. Everything else holding a person's data is in the table list of
// exportInventory, which a test checks against the schema.
type DataExporter struct {
	src ExportSource
	now func() time.Time
}

func NewDataExporter(src ExportSource) *DataExporter { return &DataExporter{src: src, now: time.Now} }

// exportInventory classifies every wallet table. Tables the person has no rows
// in are excluded with the reason.
var exportInventory = map[string]string{
	wallet.TableWallets:          "exported: wallets.json",
	wallet.TableLedger:           "exported: ledger-<wallet type>.json, the whole statement; the other side of a transfer is an opaque wallet id",
	wallet.TableIdempotency:      "excluded: request keys and cached responses, no data of the person's own",
	wallet.TablePixDeposits:      "exported: deposits.json; the payer's name and CPF only when the CPF matched the person's, never a rejected third party's",
	wallet.TableWithdrawals:      "exported: withdrawals.json (the PIX key the person typed)",
	wallet.TableUsers:            "exported: settings.json (terms, self-exclusion, limits, counters)",
	wallet.TableAudit:            "exported: audit.json, the latest events",
	wallet.TableHolds:            "exported: holds.json",
	wallet.TableSandboxPurchases: "exported: purchases-sandbox.json",
	wallet.TableProductPurchases: "exported: purchases-product.json",
	wallet.TableErasureRetention: "excluded: write-only (IAM) copy kept for the legal retention after a purge; a person with a live account has none",
	"wallet_erasure_state":       "excluded: the deletion saga's own lock/tombstone",
	"wallet_baas_accounts":       "excluded: retired table, no longer written (kept provisioned for preservation)",
	"wallet_transfer_intents":    "excluded: retired table, no longer written (kept provisioned for preservation)",
	"wallet_settlement_legs":     "excluded: retired table, no longer written (kept provisioned for preservation)",
	"wallet_med_receivables":     "excluded: retired table, no longer written (kept provisioned for preservation)",
}

// Part is the dataexport.PartFunc of the wallet. Idempotent.
func (e *DataExporter) Part(ctx context.Context, sub string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	var files, truncated []string
	write := func(name string, items any, n int, cut bool) error {
		if n == 0 {
			return nil
		}
		data, err := json.MarshalIndent(map[string]any{"truncated": cut, "items": items}, "", "  ")
		if err != nil {
			return err
		}
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		files = append(files, name)
		if cut {
			truncated = append(truncated, name)
		}
		return nil
	}

	wallets, err := e.src.Wallets(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("export: wallets: %w", err)
	}
	if err := write("wallets.json", wallets, len(wallets), false); err != nil {
		return nil, err
	}
	for _, w := range wallets {
		entries, cut, err := e.statement(ctx, w.WalletID)
		if err != nil {
			return nil, fmt.Errorf("export: statement %s: %w", w.Type, err)
		}
		if err := write("ledger-"+w.Type+".json", entries, len(entries), cut); err != nil {
			return nil, err
		}
	}

	deps, err := e.src.Deposits(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("export: deposits: %w", err)
	}
	for i := range deps {
		if deps[i].Status == wallet.DepositRejectedCPF {
			deps[i].PayerCPF, deps[i].PayerName = "", "" // a third party's, not the person's
		}
	}
	if err := write("deposits.json", deps, len(deps), false); err != nil {
		return nil, err
	}
	wds, err := e.src.Withdrawals(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("export: withdrawals: %w", err)
	}
	if err := write("withdrawals.json", wds, len(wds), false); err != nil {
		return nil, err
	}
	holds, err := e.src.Holds(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("export: holds: %w", err)
	}
	if err := write("holds.json", holds, len(holds), false); err != nil {
		return nil, err
	}

	var sandbox []wallet.SandboxPurchase
	cut, err := pageAll(func(start map[string]types.AttributeValue) ([]wallet.SandboxPurchase, map[string]types.AttributeValue, error) {
		p, err := e.src.SandboxPurchases(ctx, sub, exportPageSize, start)
		if err != nil {
			return nil, nil, err
		}
		return p.Items, p.LastEvaluatedKey, nil
	}, &sandbox)
	if err != nil {
		return nil, fmt.Errorf("export: sandbox purchases: %w", err)
	}
	if err := write("purchases-sandbox.json", sandbox, len(sandbox), cut); err != nil {
		return nil, err
	}
	var product []wallet.ProductPurchase
	cut, err = pageAll(func(start map[string]types.AttributeValue) ([]wallet.ProductPurchase, map[string]types.AttributeValue, error) {
		p, err := e.src.ProductPurchases(ctx, sub, exportPageSize, start)
		if err != nil {
			return nil, nil, err
		}
		return p.Items, p.LastEvaluatedKey, nil
	}, &product)
	if err != nil {
		return nil, fmt.Errorf("export: product purchases: %w", err)
	}
	if err := write("purchases-product.json", product, len(product), cut); err != nil {
		return nil, err
	}

	u, err := e.src.User(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("export: settings: %w", err)
	}
	if u != nil {
		if err := write("settings.json", []any{settingsView(u)}, 1, false); err != nil {
			return nil, err
		}
	}
	audit, err := e.src.Audit(ctx, sub, auditExportLimit)
	if err != nil {
		return nil, fmt.Errorf("export: audit: %w", err)
	}
	if err := write("audit.json", audit, len(audit), len(audit) >= auditExportLimit); err != nil {
		return nil, err
	}

	w, err := zw.Create("README.txt")
	if err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte(e.readme(files, truncated))); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// statement pages a wallet's ledger, newest first, until exportMaxItems.
func (e *DataExporter) statement(ctx context.Context, walletID string) ([]wallet.LedgerEntry, bool, error) {
	var out []wallet.LedgerEntry
	var start map[string]types.AttributeValue
	for {
		res, err := e.src.Statement(ctx, walletID, exportPageSize, start)
		if err != nil {
			return nil, false, err
		}
		items, err := repositories.DecodeItems[wallet.LedgerEntry](res.Items)
		if err != nil {
			return nil, false, err
		}
		for _, it := range items {
			if len(out) >= exportMaxItems {
				return out, true, nil
			}
			out = append(out, it)
		}
		if len(res.LastEvaluatedKey) == 0 {
			return out, false, nil
		}
		start = res.LastEvaluatedKey
	}
}

// pageAll collects pages into out until exportMaxItems and reports whether it stopped early.
func pageAll[T any](next func(start map[string]types.AttributeValue) ([]T, map[string]types.AttributeValue, error), out *[]T) (bool, error) {
	var start map[string]types.AttributeValue
	for {
		items, last, err := next(start)
		if err != nil {
			return false, err
		}
		for _, it := range items {
			if len(*out) >= exportMaxItems {
				return true, nil
			}
			*out = append(*out, it)
		}
		if len(last) == 0 {
			return false, nil
		}
		start = last
	}
}

// settingsView is the person's wallet-user row. The model hides it from JSON
// (json:"-"), so the export names every field it includes.
func settingsView(u *wallet.User) map[string]any {
	return map[string]any{
		"terms_addendum_version": u.TermsAddendumVersion, "terms_accepted_at": u.TermsAcceptedAt,
		"gambling_addendum_version": u.GamblingAddendumVersion, "gambling_activated_at": u.GamblingActivatedAt,
		"created_at": u.CreatedAt, "self_exclusion": u.SelfExclusion, "game_limits": u.GameLimits,
		"game_deposit_counters": u.GameDepositCounters, "real_daily_counters": u.RealDailyCounters,
	}
}

func (e *DataExporter) readme(files, truncated []string) string {
	var b strings.Builder
	b.WriteString("CTech Ledger (wallet) — a copy of your personal data\nGenerated at " + e.now().UTC().Format(time.RFC3339) + "\n\nFiles:\n")
	for _, f := range files {
		b.WriteString("  " + f + "\n")
	}
	b.WriteString("\nAmounts are in centavos (sandbox wallets: credits). Each file is {truncated, items}.\n")
	b.WriteString("ledger-*.json is your whole statement per wallet; the other side of a transfer appears only as an opaque wallet id or reference.\n")
	b.WriteString("deposits.json and withdrawals.json include the PIX data you gave. A deposit refused because it came from someone else's CPF has that payer's data removed.\n")
	b.WriteString("audit.json is the latest " + fmt.Sprint(auditExportLimit) + " events.\n")
	if len(truncated) > 0 {
		b.WriteString("\nThese files were cut at the size limit: " + strings.Join(truncated, ", ") + "\n")
	}
	return b.String()
}
