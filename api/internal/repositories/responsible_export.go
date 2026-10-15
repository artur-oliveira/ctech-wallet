package repositories

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/wallet/api/internal/domain/wallet"
)

// ExportedUser is one user's responsible-gambling state, as handed to Poker when
// it takes the rules over (docs/plans/2026-10-14-wallet-ledger-p4-poker-owns-games.md).
type ExportedUser struct {
	UserID                  string                      `json:"user_id"`
	TermsAddendumVersion    string                      `json:"terms_addendum_version,omitempty"`
	TermsAcceptedAt         string                      `json:"terms_accepted_at,omitempty"`
	GamblingAddendumVersion string                      `json:"gambling_addendum_version,omitempty"`
	GamblingActivatedAt     string                      `json:"gambling_activated_at,omitempty"`
	SelfExclusion           *wallet.SelfExclusion       `json:"self_exclusion,omitempty"`
	GameLimits              *wallet.GameLimits          `json:"game_limits,omitempty"`
	GameDepositCounters     *wallet.GameDepositCounters `json:"game_deposit_counters,omitempty"`
}

// ExportedCarriedExclusion is an exclusion that outlives an erased account, keyed
// by ctech-account's CPF keyed hash.
type ExportedCarriedExclusion struct {
	CPFHMAC       string               `json:"cpf_hmac"`
	SelfExclusion wallet.SelfExclusion `json:"exclusion"`
	ExpiresAt     int64                `json:"expires_at"`
}

// ExportPage reads one page of the users table and splits it into users that hold
// any gambling state and carried exclusions. A filtered scan: a one-off export
// over a small table, not a hot path. Rows with nothing to hand over are omitted.
func (r *UserRepository) ExportPage(ctx context.Context, start map[string]types.AttributeValue, limit int) (users []ExportedUser, carried []ExportedCarriedExclusion, next map[string]types.AttributeValue, err error) {
	out, err := r.users.ScanRaw(ctx, &dynamodb.ScanInput{ExclusiveStartKey: start, Limit: aws.Int32(int32(limit))})
	if err != nil {
		return nil, nil, nil, err
	}
	for _, item := range out.Items {
		pk, _ := item["pk"].(*types.AttributeValueMemberS)
		if pk == nil {
			continue
		}
		if strings.HasPrefix(pk.Value, wallet.SelfExclusionCarryPrefix) {
			c, err := Decode[carriedExclusion](item)
			if err != nil {
				return nil, nil, nil, err
			}
			carried = append(carried, ExportedCarriedExclusion{
				CPFHMAC: strings.TrimPrefix(pk.Value, wallet.SelfExclusionCarryPrefix), SelfExclusion: c.SelfExclusion, ExpiresAt: c.TTL,
			})
			continue
		}
		u, err := Decode[wallet.User](item)
		if err != nil {
			return nil, nil, nil, err
		}
		if u.GamblingAddendumVersion == "" && u.SelfExclusion == nil && u.GameLimits == nil && u.GameDepositCounters == nil {
			continue
		}
		users = append(users, ExportedUser{
			UserID: u.UserID, TermsAddendumVersion: u.TermsAddendumVersion, TermsAcceptedAt: u.TermsAcceptedAt,
			GamblingAddendumVersion: u.GamblingAddendumVersion, GamblingActivatedAt: u.GamblingActivatedAt,
			SelfExclusion: u.SelfExclusion, GameLimits: u.GameLimits, GameDepositCounters: u.GameDepositCounters,
		})
	}
	return users, carried, out.LastEvaluatedKey, nil
}
