package services

import (
	"archive/zip"
	"cmp"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/dataexport"

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
	// maxItems overrides exportMaxItems (tests).
	maxItems int
}

func NewDataExporter(src ExportSource) *DataExporter { return &DataExporter{src: src, now: time.Now} }

// exportInventory classifies every wallet table. Tables the person has no rows
// in are excluded with the reason.
var exportInventory = map[string]string{
	wallet.TableWallets:          "exported: wallets.json",
	wallet.TableLedger:           "exported: ledger-<wallet type>.json, the whole statement; the other side of a transfer is an opaque wallet id",
	wallet.TableIdempotency:      "excluded: request keys and cached responses, no data of the person's own",
	wallet.TablePixDeposits:      "exported: deposits.json; the payer's name and CPF only on a confirmed deposit (the CPF check passed), never on any other status",
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

// exportFiles is what one export wrote, for the README.
type exportFiles struct {
	zw               *zip.Writer
	max              int // items per file
	files, truncated []string
}

// list streams one file {items, truncated} an item at a time. next yields
// pages (items and the continuation key) until the source is exhausted or
// exportMaxItems were written; a list with no items writes no file.
func list[T any](f *exportFiles, name string, next func(start map[string]types.AttributeValue) ([]T, map[string]types.AttributeValue, error)) error {
	var it *dataexport.Items
	n, cut := 0, false
	var start map[string]types.AttributeValue
paging:
	for {
		items, last, err := next(start)
		if err != nil {
			return err
		}
		for _, item := range items {
			if n >= f.max {
				cut = true
				break paging
			}
			if it == nil {
				if it, err = dataexport.BeginItems(f.zw, name); err != nil {
					return err
				}
				f.files = append(f.files, name)
			}
			if err := it.Add(item); err != nil {
				return err
			}
			n++
		}
		if len(last) == 0 {
			break
		}
		start = last
	}
	if it == nil {
		return nil
	}
	if cut {
		f.truncated = append(f.truncated, name)
	}
	return it.End(cut)
}

// whole writes a list the source answered in one call; cut is the source's own
// "there was more" (the audit trail's latest-N).
func whole[T any](f *exportFiles, name string, items []T, cut bool) error {
	if len(items) == 0 {
		return nil
	}
	it, err := dataexport.BeginItems(f.zw, name)
	if err != nil {
		return err
	}
	f.files = append(f.files, name)
	for _, item := range items {
		if err := it.Add(item); err != nil {
			return err
		}
	}
	if cut {
		f.truncated = append(f.truncated, name)
	}
	return it.End(cut)
}

// Write is the dataexport.PartStreamFunc of the wallet: it writes the zip to w
// as it reads, a page of ledger or purchases at a time, so a wallet of any size
// costs one page of memory. Idempotent.
func (e *DataExporter) Write(ctx context.Context, sub string, w io.Writer) error {
	f := &exportFiles{zw: zip.NewWriter(w), max: cmp.Or(e.maxItems, exportMaxItems)}

	wallets, err := e.src.Wallets(ctx, sub)
	if err != nil {
		return fmt.Errorf("export: wallets: %w", err)
	}
	if err := whole(f, "wallets.json", wallets, false); err != nil {
		return err
	}
	for _, wl := range wallets {
		err := list(f, "ledger-"+wl.Type+".json", func(start map[string]types.AttributeValue) ([]wallet.LedgerEntry, map[string]types.AttributeValue, error) {
			res, err := e.src.Statement(ctx, wl.WalletID, exportPageSize, start)
			if err != nil {
				return nil, nil, err
			}
			items, err := repositories.DecodeItems[wallet.LedgerEntry](res.Items)
			return items, res.LastEvaluatedKey, err
		})
		if err != nil {
			return fmt.Errorf("export: statement %s: %w", wl.Type, err)
		}
	}

	deps, err := e.src.Deposits(ctx, sub)
	if err != nil {
		return fmt.Errorf("export: deposits: %w", err)
	}
	// Allowlist, not a denylist: only a confirmed deposit passed the CPF check, so only
	// then is the payer the person. Every other status (pending, expired, the refund
	// states, the legacy rejection) may carry a third party's name and CPF.
	for i := range deps {
		if deps[i].Status != wallet.DepositConfirmed {
			deps[i].PayerCPF, deps[i].PayerName = "", ""
		}
	}
	if err := whole(f, "deposits.json", deps, false); err != nil {
		return err
	}
	wds, err := e.src.Withdrawals(ctx, sub)
	if err != nil {
		return fmt.Errorf("export: withdrawals: %w", err)
	}
	if err := whole(f, "withdrawals.json", wds, false); err != nil {
		return err
	}
	holds, err := e.src.Holds(ctx, sub)
	if err != nil {
		return fmt.Errorf("export: holds: %w", err)
	}
	if err := whole(f, "holds.json", holds, false); err != nil {
		return err
	}

	if err := list(f, "purchases-sandbox.json", func(start map[string]types.AttributeValue) ([]wallet.SandboxPurchase, map[string]types.AttributeValue, error) {
		p, err := e.src.SandboxPurchases(ctx, sub, exportPageSize, start)
		if err != nil {
			return nil, nil, err
		}
		return p.Items, p.LastEvaluatedKey, nil
	}); err != nil {
		return fmt.Errorf("export: sandbox purchases: %w", err)
	}
	if err := list(f, "purchases-product.json", func(start map[string]types.AttributeValue) ([]wallet.ProductPurchase, map[string]types.AttributeValue, error) {
		p, err := e.src.ProductPurchases(ctx, sub, exportPageSize, start)
		if err != nil {
			return nil, nil, err
		}
		return p.Items, p.LastEvaluatedKey, nil
	}); err != nil {
		return fmt.Errorf("export: product purchases: %w", err)
	}

	u, err := e.src.User(ctx, sub)
	if err != nil {
		return fmt.Errorf("export: settings: %w", err)
	}
	if u != nil {
		if err := whole(f, "settings.json", []any{settingsView(u)}, false); err != nil {
			return err
		}
	}
	audit, err := e.src.Audit(ctx, sub, auditExportLimit)
	if err != nil {
		return fmt.Errorf("export: audit: %w", err)
	}
	if err := whole(f, "audit.json", audit, len(audit) >= auditExportLimit); err != nil {
		return err
	}

	rw, err := f.zw.Create("README.txt")
	if err != nil {
		return err
	}
	if _, err := rw.Write([]byte(e.readme(f.files, f.truncated))); err != nil {
		return err
	}
	return f.zw.Close()
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
	b.WriteString("\nAmounts are in centavos (sandbox wallets: credits). Each file is {items, truncated}.\n")
	b.WriteString("ledger-*.json is your whole statement per wallet; the other side of a transfer appears only as an opaque wallet id or reference.\n")
	b.WriteString("deposits.json and withdrawals.json include the PIX data you gave. The payer's name and CPF appear only on confirmed deposits; on any other status they are removed, because they may be someone else's.\n")
	b.WriteString("audit.json is the latest " + fmt.Sprint(auditExportLimit) + " events.\n")
	if len(truncated) > 0 {
		b.WriteString("\nThese files were cut at the size limit: " + strings.Join(truncated, ", ") + "\n")
	}
	return b.String()
}
