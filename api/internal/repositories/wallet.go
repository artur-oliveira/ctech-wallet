package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/id"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
	"gopkg.aoctech.app/wallet/api/internal/problem"
)

// idemTTLDays is how long idempotency guard rows live before Dynamo TTL reaps them.
const idemTTLDays = 7

// WalletRepository owns all wallet persistence: balances (authoritative),
// the append-only ledger, idempotency guards, PIX deposits, and withdrawals.
// Every balance mutation is a single conditional TransactWriteItems.
type WalletRepository struct {
	wallets    Base
	ledger     Base
	idem       Base
	deposits   Base
	withdrawal Base
	holds      Base
}

// NewWalletRepository builds the repository with one Base per wallet table.
func NewWalletRepository(db *dynamodb.Client, cfg *config.Config) *WalletRepository {
	return &WalletRepository{
		wallets:    NewBase(db, cfg, wallet.TableWallets),
		ledger:     NewBase(db, cfg, wallet.TableLedger),
		idem:       NewBase(db, cfg, wallet.TableIdempotency),
		deposits:   NewBase(db, cfg, wallet.TablePixDeposits),
		withdrawal: NewBase(db, cfg, wallet.TableWithdrawals),
		holds:      NewBase(db, cfg, wallet.TableHolds),
	}
}

// idemGuard is the idempotency guard row stored in the idempotency table.
type idemGuard struct {
	PK        string `dynamodbav:"pk"`
	WalletID  string `dynamodbav:"wallet_id"`
	EntrySK   string `dynamodbav:"entry_sk"`
	ReqHash   string `dynamodbav:"req_hash"`
	CreatedAt string `dynamodbav:"created_at"`
	TTL       int64  `dynamodbav:"ttl"`
}

// walletMarker is the (user_id, type) uniqueness guard so a user always has
// exactly one real and one sandbox wallet even under concurrent first access.
type walletMarker struct {
	PK        string `dynamodbav:"pk"`
	WalletID  string `dynamodbav:"wallet_id"`
	Type      string `dynamodbav:"type"`
	CreatedAt string `dynamodbav:"created_at"`
}

func markerPK(userID, walletType string) string {
	return "USER#" + userID + "#" + walletType
}

// Mutation describes a single-wallet balance change (credit or debit).
type Mutation struct {
	WalletID       string
	Amount         int64 // positive magnitude
	EntryType      string
	Ref            string
	IdempotencyKey string
	ReqHash        string
}

// GetWallet returns the authoritative wallet record, or nil if absent.
func (r *WalletRepository) GetWallet(ctx context.Context, walletID string) (*wallet.Wallet, error) {
	item, err := r.wallets.GetItem(ctx, walletID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}
	return Decode[wallet.Wallet](item)
}

// GetWalletsByUser returns the user's wallets via the user GSI (excludes marker rows).
func (r *WalletRepository) GetWalletsByUser(ctx context.Context, userID string) ([]wallet.Wallet, error) {
	res, err := r.wallets.QueryGSI(ctx, wallet.GSIUser, "user_id", userID, 10, nil)
	if err != nil {
		return nil, err
	}
	out := make([]wallet.Wallet, 0, len(res.Items))
	for _, it := range res.Items {
		w, err := Decode[wallet.Wallet](it)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, nil
}

// LoadWallets returns the user's wallets by type. game and sandbox are nil until
// the user activates gambling — their absence IS the "not activated" signal.
func (r *WalletRepository) LoadWallets(ctx context.Context, userID string) (real, game, sandbox *wallet.Wallet, err error) {
	byType, err := r.loadByMarkers(ctx, userID, wallet.TypeReal, wallet.TypeGame, wallet.TypeSandbox)
	if err != nil {
		return nil, nil, nil, err
	}
	return byType[wallet.TypeReal], byType[wallet.TypeGame], byType[wallet.TypeSandbox], nil
}

// EnsureRealWallet returns the user's real wallet, creating it on first access.
// It does NOT create game or sandbox: those exist only after explicit activation,
// so a user who never gambles never gets a gambling wallet. Concurrent callers
// converge via the (user_id, type) marker guard.
func (r *WalletRepository) EnsureRealWallet(ctx context.Context, userID string) (*wallet.Wallet, error) {
	byType, err := r.EnsureWalletsOfType(ctx, userID, wallet.TypeReal)
	if err != nil {
		return nil, err
	}
	return byType[wallet.TypeReal], nil
}

// EnsureWalletsOfType returns the user's wallets of the given types, creating any
// that are missing in ONE transaction. Existing wallets are always REUSED, never
// replaced — replacing one would orphan its balance.
func (r *WalletRepository) EnsureWalletsOfType(ctx context.Context, userID string, walletTypes ...string) (map[string]*wallet.Wallet, error) {
	byType, err := r.loadByMarkers(ctx, userID, walletTypes...)
	if err != nil {
		return nil, err
	}
	missing := make([]string, 0, len(walletTypes))
	for _, typ := range walletTypes {
		if byType[typ] == nil {
			missing = append(missing, typ)
		}
	}
	if len(missing) == 0 {
		return byType, nil
	}

	created, err := r.createWallets(ctx, userID, missing...)
	if err != nil {
		return nil, err
	}
	for typ, w := range created {
		byType[typ] = w
	}
	return byType, nil
}

// EnsureGamblingWallets creates the game and sandbox wallets together, atomically,
// and is idempotent: a second call converges on what the first created, and a
// pre-existing sandbox wallet (from the old two-wallet model) is REUSED rather
// than replaced — replacing it would orphan its balance.
//
// Callers MUST gate on KYC and gambling-addendum acceptance before calling this:
// the repository enforces persistence, not policy.
func (r *WalletRepository) EnsureGamblingWallets(ctx context.Context, userID string) (game, sandbox *wallet.Wallet, err error) {
	byType, err := r.EnsureWalletsOfType(ctx, userID, wallet.TypeGame, wallet.TypeSandbox)
	if err != nil {
		return nil, nil, err
	}
	return byType[wallet.TypeGame], byType[wallet.TypeSandbox], nil
}

// createWallets writes the given wallet types (plus their markers) in ONE
// transaction, so a partial set can never exist. On a lost race it re-reads what
// the winner created.
func (r *WalletRepository) createWallets(ctx context.Context, userID string, walletTypes ...string) (map[string]*wallet.Wallet, error) {
	now := NowStr()
	out := make(map[string]*wallet.Wallet, len(walletTypes))
	items := make([]types.TransactWriteItem, 0, len(walletTypes)*2)
	for _, typ := range walletTypes {
		w := wallet.Wallet{
			WalletID: wallet.WalletPrefix + id.New(), UserID: userID, Type: typ,
			Balance: 0, Version: 0, CreatedAt: now, UpdatedAt: now,
		}
		wav, err := Encode(w)
		if err != nil {
			return nil, err
		}
		mav, err := Encode(walletMarker{PK: markerPK(userID, typ), WalletID: w.WalletID, Type: typ, CreatedAt: now})
		if err != nil {
			return nil, err
		}
		items = append(items, r.wallets.BuildPutTxItemIfAbsent(wav), r.wallets.BuildPutTxItemIfAbsent(mav))
		out[typ] = &w
	}

	if err := r.wallets.TransactWrite(ctx, items); err != nil {
		if IsConditionFailed(err) {
			// A concurrent caller won the race — read what it created.
			return r.loadByMarkers(ctx, userID, walletTypes...)
		}
		return nil, err
	}
	return out, nil
}

// loadByMarkers resolves the requested wallet types via their (user_id, type)
// markers. A type with no marker is simply absent from the map.
func (r *WalletRepository) loadByMarkers(ctx context.Context, userID string, walletTypes ...string) (map[string]*wallet.Wallet, error) {
	out := make(map[string]*wallet.Wallet, len(walletTypes))
	for _, typ := range walletTypes {
		mItem, err := r.wallets.GetItem(ctx, markerPK(userID, typ))
		if err != nil {
			return nil, err
		}
		if mItem == nil {
			continue
		}
		m, err := Decode[walletMarker](mItem)
		if err != nil {
			return nil, err
		}
		w, err := r.GetWallet(ctx, m.WalletID)
		if err != nil {
			return nil, err
		}
		out[typ] = w
	}
	return out, nil
}

// Credit adds Amount to a wallet. Debit subtracts it with a balance>=Amount
// condition. Both co-write the ledger entry and an idempotency guard in one
// TransactWriteItems. On replay (same key) they return the prior entry.
func (r *WalletRepository) Credit(ctx context.Context, m Mutation) (*wallet.LedgerEntry, bool, error) {
	return r.mutate(ctx, m, +1)
}

func (r *WalletRepository) Debit(ctx context.Context, m Mutation) (*wallet.LedgerEntry, bool, error) {
	return r.mutate(ctx, m, -1)
}

// mutate applies a signed single-wallet balance change. sign is +1 (credit) or -1 (debit).
func (r *WalletRepository) mutate(ctx context.Context, m Mutation, sign int64) (*wallet.LedgerEntry, bool, error) {
	prior, conflict, err := r.checkReplay(ctx, m.IdempotencyKey, m.ReqHash)
	if err != nil {
		return nil, false, err
	}
	if conflict != nil {
		return nil, false, conflict
	}
	if prior != nil {
		return prior, true, nil
	}

	w, err := r.GetWallet(ctx, m.WalletID)
	if err != nil {
		return nil, false, err
	}
	if w == nil {
		return nil, false, problem.NotFound("carteira não encontrada")
	}

	signed := sign * m.Amount
	entry := r.newEntry(m.WalletID, m.EntryType, signed, w.Balance+signed, m.IdempotencyKey, m.Ref)

	walletTx, err := r.balanceTx(m.WalletID, m.Amount, sign)
	if err != nil {
		return nil, false, err
	}
	ledgerTx, guardTx, err := r.ledgerAndGuardTx(entry, m.IdempotencyKey, m.ReqHash)
	if err != nil {
		return nil, false, err
	}

	if err := r.wallets.TransactWrite(ctx, []types.TransactWriteItem{walletTx, ledgerTx, guardTx}); err != nil {
		return r.resolveTxErr(ctx, m.IdempotencyKey, m.ReqHash, sign, err)
	}
	return entry, false, nil
}

// DebitWithFee debits amount+fee in one transaction, writing a withdraw entry and
// a fee entry plus the idempotency guard. Used by the withdrawal flow.
func (r *WalletRepository) DebitWithFee(ctx context.Context, walletID string, amount, fee int64, idemKey, reqHash, ref string) (withdrawEntry, feeEntry *wallet.LedgerEntry, replayed bool, err error) {
	prior, conflict, e := r.checkReplay(ctx, idemKey, reqHash)
	if e != nil {
		return nil, nil, false, e
	}
	if conflict != nil {
		return nil, nil, false, conflict
	}
	if prior != nil {
		// On replay we return the withdraw entry as the primary; the fee entry is audit-only.
		return prior, nil, true, nil
	}
	w, err := r.GetWallet(ctx, walletID)
	if err != nil {
		return nil, nil, false, err
	}
	if w == nil {
		return nil, nil, false, problem.NotFound("carteira não encontrada")
	}
	total := amount + fee
	wEntry := r.newEntry(walletID, wallet.EntryWithdraw, -amount, w.Balance-total, idemKey, ref)
	fEntry := r.newEntry(walletID, wallet.EntryFee, -fee, w.Balance-total, idemKey, ref)

	walletTx, err := r.balanceTx(walletID, total, -1)
	if err != nil {
		return nil, nil, false, err
	}
	wLedger := r.ledger.BuildPutTxItemIfAbsent(mustEncode(wEntry))
	fLedger := r.ledger.BuildPutTxItemIfAbsent(mustEncode(fEntry))
	guardTx, err := r.guardTx(walletID, wEntry.SK, idemKey, reqHash)
	if err != nil {
		return nil, nil, false, err
	}
	if err := r.wallets.TransactWrite(ctx, []types.TransactWriteItem{walletTx, wLedger, fLedger, guardTx}); err != nil {
		e, _, err2 := r.resolveTxErr(ctx, idemKey, reqHash, -1, err)
		return e, nil, e != nil, err2
	}
	return wEntry, fEntry, false, nil
}

// Transfer atomically debits fromWalletID and credits toWalletID by the same
// amount, writing two ledger entries and one idempotency guard. Used by sandbox
// purchase (real → sandbox).
func (r *WalletRepository) Transfer(ctx context.Context, fromWalletID, toWalletID string, amount int64, debitType, creditType, ref, idemKey, reqHash string) (debit, credit *wallet.LedgerEntry, replayed bool, err error) {
	prior, conflict, e := r.checkReplay(ctx, idemKey, reqHash)
	if e != nil {
		return nil, nil, false, e
	}
	if conflict != nil {
		return nil, nil, false, conflict
	}
	if prior != nil {
		return prior, nil, true, nil
	}
	from, err := r.GetWallet(ctx, fromWalletID)
	if err != nil {
		return nil, nil, false, err
	}
	to, err := r.GetWallet(ctx, toWalletID)
	if err != nil {
		return nil, nil, false, err
	}
	if from == nil || to == nil {
		return nil, nil, false, problem.NotFound("carteira não encontrada")
	}
	dEntry := r.newEntry(fromWalletID, debitType, -amount, from.Balance-amount, idemKey, ref)
	cEntry := r.newEntry(toWalletID, creditType, +amount, to.Balance+amount, idemKey, ref)

	debitTx, err := r.balanceTx(fromWalletID, amount, -1)
	if err != nil {
		return nil, nil, false, err
	}
	creditTx, err := r.balanceTx(toWalletID, amount, +1)
	if err != nil {
		return nil, nil, false, err
	}
	guardTx, err := r.guardTx(fromWalletID, dEntry.SK, idemKey, reqHash)
	if err != nil {
		return nil, nil, false, err
	}
	items := []types.TransactWriteItem{
		debitTx, creditTx,
		r.ledger.BuildPutTxItemIfAbsent(mustEncode(dEntry)),
		r.ledger.BuildPutTxItemIfAbsent(mustEncode(cEntry)),
		guardTx,
	}
	if err := r.wallets.TransactWrite(ctx, items); err != nil {
		e, _, err2 := r.resolveTxErr(ctx, idemKey, reqHash, -1, err)
		return e, nil, e != nil, err2
	}
	return dEntry, cEntry, false, nil
}

// Statement returns ledger entries for a wallet, newest first, paginated.
func (r *WalletRepository) Statement(ctx context.Context, walletID string, limit int, startKey map[string]types.AttributeValue) (*QueryResult, error) {
	return r.ledger.Query(ctx, QueryOpts{
		PK:                walletID,
		ScanIndexForward:  false,
		Limit:             limit,
		ExclusiveStartKey: startKey,
	})
}

// --- PIX deposit persistence ---

func (r *WalletRepository) PutDeposit(ctx context.Context, d *wallet.PixDeposit) error {
	av, err := Encode(d)
	if err != nil {
		return err
	}
	return r.deposits.PutItem(ctx, av)
}

func (r *WalletRepository) GetDeposit(ctx context.Context, txid string) (*wallet.PixDeposit, error) {
	item, err := r.deposits.GetItem(ctx, txid)
	if err != nil || item == nil {
		return nil, err
	}
	return Decode[wallet.PixDeposit](item)
}

func (r *WalletRepository) UpdateDepositStatus(ctx context.Context, txid, status, e2eID string) error {
	_, err := r.deposits.UpdateItem(ctx, txid, nil, map[string]any{
		"status": status,
		"e2e_id": e2eID,
	})
	return err
}

// UpdateDepositPayer persists the payer CPF/name reported by the webhook — the
// charge re-query no longer returns them, so this is their only source.
func (r *WalletRepository) UpdateDepositPayer(ctx context.Context, txid, payerCPF, payerName string) error {
	_, err := r.deposits.UpdateItem(ctx, txid, nil, map[string]any{
		"payer_cpf":  payerCPF,
		"payer_name": payerName,
	})
	return err
}

// ListPendingDepositsOlderThan returns pending deposits created before cutoff —
// the sweep's work queue (F6): a pending deposit that never got a webhook has
// no fallback path to eventual consistency before its TTL deletes the row, so
// the sweep re-queries Inter once before that happens.
func (r *WalletRepository) ListPendingDepositsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]wallet.PixDeposit, error) {
	res, err := r.deposits.QueryGSI(ctx, wallet.GSIStatus, "status", wallet.DepositPending, limit, nil)
	if err != nil {
		return nil, err
	}
	out := make([]wallet.PixDeposit, 0, len(res.Items))
	for _, it := range res.Items {
		d, err := Decode[wallet.PixDeposit](it)
		if err != nil {
			return nil, err
		}
		createdAt, err := time.Parse(time.RFC3339Nano, d.CreatedAt)
		if err != nil {
			continue // malformed timestamp — skip rather than fail the whole sweep
		}
		if createdAt.Before(cutoff) {
			out = append(out, *d)
		}
	}
	return out, nil
}

// --- withdrawal persistence ---

// ErrWithdrawalExists means PutWithdrawal lost a race — another call already
// created this withdrawal record. The caller must re-fetch and return the
// winner's record instead of retrying the write or the PIX transfer.
var ErrWithdrawalExists = errors.New("repositories: withdrawal already exists")

func (r *WalletRepository) PutWithdrawal(ctx context.Context, w *wallet.Withdrawal) error {
	av, err := Encode(w)
	if err != nil {
		return err
	}
	item := r.withdrawal.BuildPutTxItemIfAbsent(av)
	if err := r.withdrawal.TransactWrite(ctx, []types.TransactWriteItem{item}); err != nil {
		if IsConditionFailed(err) {
			return ErrWithdrawalExists
		}
		return err
	}
	return nil
}

func (r *WalletRepository) GetWithdrawal(ctx context.Context, withdrawalID string) (*wallet.Withdrawal, error) {
	item, err := r.withdrawal.GetItem(ctx, withdrawalID)
	if err != nil || item == nil {
		return nil, err
	}
	return Decode[wallet.Withdrawal](item)
}

func (r *WalletRepository) UpdateWithdrawal(ctx context.Context, withdrawalID string, updates map[string]any) error {
	updates["updated_at"] = NowStr()
	_, err := r.withdrawal.UpdateItem(ctx, withdrawalID, nil, updates)
	return err
}

// ListProcessingWithdrawals returns withdrawals still in the processing state,
// via the status GSI — the reconciliation job's work queue.
func (r *WalletRepository) ListProcessingWithdrawals(ctx context.Context, limit int) ([]wallet.Withdrawal, error) {
	res, err := r.withdrawal.QueryGSI(ctx, wallet.GSIStatus, "status", wallet.WithdrawProcessing, limit, nil)
	if err != nil {
		return nil, err
	}
	out := make([]wallet.Withdrawal, 0, len(res.Items))
	for _, it := range res.Items {
		w, err := Decode[wallet.Withdrawal](it)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, nil
}

// --- shared transaction builders ---

func (r *WalletRepository) newEntry(walletID, entryType string, signedAmount, balanceAfter int64, idemKey, ref string) *wallet.LedgerEntry {
	entryID := id.New()
	ts := NowStr()
	return &wallet.LedgerEntry{
		WalletID:       walletID,
		SK:             ts + "#" + entryID,
		EntryID:        entryID,
		Type:           entryType,
		Amount:         signedAmount,
		BalanceAfter:   balanceAfter,
		IdempotencyKey: idemKey,
		Ref:            ref,
		CreatedAt:      ts,
	}
}

// balanceTx builds the conditional wallet balance update. sign +1 credits,
// -1 debits (with a balance>=amount guard so the balance never goes negative).
func (r *WalletRepository) balanceTx(walletID string, amount int64, sign int64) (types.TransactWriteItem, error) {
	op := "+"
	cond := "attribute_exists(pk)"
	if sign < 0 {
		op = "-"
		cond = "attribute_exists(pk) AND balance >= :amt"
	}
	updateExpr := fmt.Sprintf("SET balance = balance %s :amt, version = version + :one, updated_at = :now", op)
	values := map[string]types.AttributeValue{
		":amt": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", amount)},
		":one": &types.AttributeValueMemberN{Value: "1"},
		":now": &types.AttributeValueMemberS{Value: NowStr()},
	}
	// No #-aliased names in this expression — pass nil (an empty map is rejected
	// by DynamoDB as "ExpressionAttributeNames must not be empty").
	return r.wallets.BuildRawUpdateTxItem(walletID, nil, updateExpr, cond, nil, values), nil
}

func (r *WalletRepository) ledgerAndGuardTx(entry *wallet.LedgerEntry, idemKey, reqHash string) (ledgerTx, guardTx types.TransactWriteItem, err error) {
	lav, err := Encode(entry)
	if err != nil {
		return ledgerTx, guardTx, err
	}
	ledgerTx = r.ledger.BuildPutTxItemIfAbsent(lav)
	guardTx, err = r.guardTx(entry.WalletID, entry.SK, idemKey, reqHash)
	return ledgerTx, guardTx, err
}

func (r *WalletRepository) guardTx(walletID, entrySK, idemKey, reqHash string) (types.TransactWriteItem, error) {
	g := idemGuard{
		PK:        wallet.IdemPrefix + idemKey,
		WalletID:  walletID,
		EntrySK:   entrySK,
		ReqHash:   reqHash,
		CreatedAt: NowStr(),
		TTL:       time.Now().Add(idemTTLDays * 24 * time.Hour).Unix(),
	}
	gav, err := Encode(g)
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	return r.idem.BuildPutTxItemIfAbsent(gav), nil
}

// checkReplay returns (priorEntry, conflictProblem, err). A prior entry means
// the same key already committed with a matching request hash; a conflict means
// the same key was used with a different payload.
func (r *WalletRepository) checkReplay(ctx context.Context, idemKey, reqHash string) (*wallet.LedgerEntry, *problem.Problem, error) {
	item, err := r.idem.GetItem(ctx, wallet.IdemPrefix+idemKey)
	if err != nil {
		return nil, nil, err
	}
	if item == nil {
		return nil, nil, nil
	}
	g, err := Decode[idemGuard](item)
	if err != nil {
		return nil, nil, err
	}
	if g.ReqHash != reqHash {
		return nil, problem.IdempotencyConflict(), nil
	}
	entry, err := r.loadEntry(ctx, g.WalletID, g.EntrySK)
	if err != nil {
		return nil, nil, err
	}
	return entry, nil, nil
}

func (r *WalletRepository) loadEntry(ctx context.Context, walletID, sk string) (*wallet.LedgerEntry, error) {
	item, err := r.ledger.GetItem(ctx, walletID, sk)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}
	return Decode[wallet.LedgerEntry](item)
}

// resolveTxErr disambiguates a failed transaction: a now-present guard means a
// concurrent identical request won (replay); otherwise a failed condition on a
// debit is an insufficient balance.
func (r *WalletRepository) resolveTxErr(ctx context.Context, idemKey, reqHash string, sign int64, txErr error) (*wallet.LedgerEntry, bool, error) {
	if !IsConditionFailed(txErr) {
		return nil, false, txErr
	}
	prior, conflict, err := r.checkReplay(ctx, idemKey, reqHash)
	if err != nil {
		return nil, false, err
	}
	if conflict != nil {
		return nil, false, conflict
	}
	if prior != nil {
		return prior, true, nil
	}
	if sign < 0 {
		return nil, false, problem.InsufficientBalance()
	}
	return nil, false, problem.InternalServer("transação de crédito falhou na condição")
}

func mustEncode(v any) map[string]types.AttributeValue {
	av, err := Encode(v)
	if err != nil {
		panic(err)
	}
	return av
}
