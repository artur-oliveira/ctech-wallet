package repositories

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

// ListWalletsOfType pages the wallets of one type. It is a filtered scan of the
// wallets table, used only by the one-off game-merge command: at the current
// closed-group size a scan is cheaper than an index nothing else would read.
func (r *WalletRepository) ListWalletsOfType(ctx context.Context, walletType string, start map[string]types.AttributeValue, limit int) (*Page[wallet.Wallet], error) {
	out, err := r.wallets.ScanRaw(ctx, &dynamodb.ScanInput{
		FilterExpression:          aws.String("#t = :t AND begins_with(pk, :w)"),
		ExpressionAttributeNames:  map[string]string{"#t": "type"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":t": &types.AttributeValueMemberS{Value: walletType}, ":w": &types.AttributeValueMemberS{Value: wallet.WalletPrefix}},
		ExclusiveStartKey:         start,
		Limit:                     aws.Int32(int32(limit)),
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[wallet.Wallet](out.Items)
	if err != nil {
		return nil, err
	}
	return &Page[wallet.Wallet]{Items: rows, LastEvaluatedKey: out.LastEvaluatedKey}, nil
}

// RetireWallet marks an emptied wallet as retired. It only succeeds on a wallet
// whose balance is zero and that is not retired yet, so a wallet that still
// holds money, or a replay, is a no-op (false, nil).
func (r *WalletRepository) RetireWallet(ctx context.Context, walletID string) (bool, error) {
	_, err := r.wallets.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		Key:                       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: walletID}},
		UpdateExpression:          aws.String("SET retired_at = :now, updated_at = :now"),
		ConditionExpression:       aws.String("attribute_exists(pk) AND (attribute_not_exists(balance) OR balance = :zero) AND attribute_not_exists(retired_at)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":now": &types.AttributeValueMemberS{Value: NowStr()}, ":zero": &types.AttributeValueMemberN{Value: "0"}},
	})
	if IsConditionFailed(err) {
		return false, nil
	}
	return err == nil, err
}

// UnretireWallet reverses RetireWallet for the rollback of a merge.
func (r *WalletRepository) UnretireWallet(ctx context.Context, walletID string) error {
	_, err := r.wallets.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		Key:                       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: walletID}},
		UpdateExpression:          aws.String("REMOVE retired_at SET updated_at = :now"),
		ConditionExpression:       aws.String("attribute_exists(pk)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":now": &types.AttributeValueMemberS{Value: NowStr()}},
	})
	return err
}
