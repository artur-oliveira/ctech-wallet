package repositories

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"gopkg.aoctech.app/wallet/api/internal/config"
	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

func newUnitUserRepo() *UserRepository {
	return NewUserRepository((*dynamodb.Client)(nil), &config.Config{TablePrefix: "test"})
}

func TestBumpDepositCountersFreshRowConditionsOnAbsence(t *testing.T) {
	r := newUnitUserRepo()
	item, err := r.BumpDepositCounters("u1", nil, wallet.GameDepositCounters{DayKey: "2026-07-19", DaySum: 100})
	if err != nil {
		t.Fatal(err)
	}
	u := item.Update
	if u == nil {
		t.Fatal("expected an Update transact item")
	}
	if got := *u.ConditionExpression; got != "attribute_not_exists(#c)" {
		t.Fatalf("condition = %q", got)
	}
	if !strings.Contains(*u.UpdateExpression, "#c = :next") {
		t.Fatalf("update = %q", *u.UpdateExpression)
	}
	if u.ExpressionAttributeNames["#c"] != "game_deposit_counters" {
		t.Fatalf("names = %v", u.ExpressionAttributeNames)
	}
}

func TestBumpDepositCountersConditionsOnPreviousValue(t *testing.T) {
	r := newUnitUserRepo()
	prev := &wallet.GameDepositCounters{DayKey: "2026-07-19", DaySum: 50}
	item, err := r.BumpDepositCounters("u1", prev, wallet.GameDepositCounters{DayKey: "2026-07-19", DaySum: 150})
	if err != nil {
		t.Fatal(err)
	}
	u := item.Update
	if got := *u.ConditionExpression; got != "#c = :prev" {
		t.Fatalf("condition = %q", got)
	}
	if _, ok := u.ExpressionAttributeValues[":prev"]; !ok {
		t.Fatal("missing :prev value")
	}
}
