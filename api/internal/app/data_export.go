package app

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
	"gopkg.aoctech.app/wallet/api/internal/services"
)

// exportSources adapts the repositories to services.ExportSource, so the
// personal-data export reads exactly what the API stores.
type exportSources struct {
	wallets  *repositories.WalletRepository
	users    *repositories.UserRepository
	audit    *repositories.AuditRepository
	sandbox  *repositories.SandboxPurchaseRepository
	products *repositories.ProductPurchaseRepository
	erasure  *repositories.ErasureRepository
}

func (s exportSources) Wallets(ctx context.Context, u string) ([]wallet.Wallet, error) {
	return s.wallets.GetWalletsByUser(ctx, u)
}

func (s exportSources) Statement(ctx context.Context, id string, limit int, start map[string]types.AttributeValue) (*repositories.QueryResult, error) {
	return s.wallets.Statement(ctx, id, limit, start)
}

func (s exportSources) Deposits(ctx context.Context, u string) ([]wallet.PixDeposit, error) {
	return s.erasure.DepositsForUser(ctx, u)
}

func (s exportSources) Withdrawals(ctx context.Context, u string) ([]wallet.Withdrawal, error) {
	return s.erasure.WithdrawalsForUser(ctx, u)
}

func (s exportSources) Holds(ctx context.Context, u string) ([]wallet.Hold, error) {
	return s.erasure.HoldsForUser(ctx, u)
}

func (s exportSources) SandboxPurchases(ctx context.Context, u string, limit int, start map[string]types.AttributeValue) (*repositories.Page[wallet.SandboxPurchase], error) {
	return s.sandbox.ListByUser(ctx, u, limit, start)
}

func (s exportSources) ProductPurchases(ctx context.Context, u string, limit int, start map[string]types.AttributeValue) (*repositories.Page[wallet.ProductPurchase], error) {
	return s.products.ListByUser(ctx, u, limit, start)
}

func (s exportSources) User(ctx context.Context, u string) (*wallet.User, error) {
	return s.users.Get(ctx, u)
}

func (s exportSources) Audit(ctx context.Context, u string, limit int) ([]wallet.AuditEvent, error) {
	return s.audit.List(ctx, u, limit)
}

var _ services.ExportSource = exportSources{}

func newDataExporter(w *repositories.WalletRepository, u *repositories.UserRepository, a *repositories.AuditRepository,
	sp *repositories.SandboxPurchaseRepository, pp *repositories.ProductPurchaseRepository, er *repositories.ErasureRepository) *services.DataExporter {
	return services.NewDataExporter(exportSources{wallets: w, users: u, audit: a, sandbox: sp, products: pp, erasure: er})
}
