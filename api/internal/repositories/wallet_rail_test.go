package repositories

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

func newUnitWalletRepo() *WalletRepository {
	return NewWalletRepository((*dynamodb.Client)(nil), &config.Config{TablePrefix: "test"})
}

func TestWithdrawalPutTxIsConditionalPut(t *testing.T) {
	r := newUnitWalletRepo()
	item, err := r.WithdrawalPutTx(&wallet.Withdrawal{WithdrawalID: "withdraw#u1#k1", WalletID: "w1", UserID: "u1", Amount: 100})
	if err != nil {
		t.Fatal(err)
	}
	if item.Put == nil || item.Put.ConditionExpression == nil {
		t.Fatalf("want a conditional put, got %+v", item)
	}
}
