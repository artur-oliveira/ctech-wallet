// Package repositories provides DynamoDB persistence layer, mirroring
// api/app/repositories/base.py.
//
// Key design rules (from CLAUDE.md):
//   - get_item > query > scan  (no scans in production)
//   - transact_write for NF-e numbering atomicity
//   - Table names are prefixed by environment: {prefix}_{table}
package repositories

import (
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/dynamo"
	"gopkg.aoctech.app/wallet/api/internal/config"
)

// Base provides common DynamoDB operations for all repositories.
type Base = dynamo.Base

// QueryResult holds paginated query results.
type QueryResult = dynamo.QueryResult

// QueryOpts configures a Query call.
type QueryOpts = dynamo.QueryOpts

// NowStr returns the current UTC time as ISO 8601, matching Python's now_str().
var NowStr = dynamo.NowStr

// MarshalMapOmitNull marshals v into a DynamoDB attribute map, omitting any
// attribute whose value is null (recursively, including nested maps and list
// elements).
var MarshalMapOmitNull = dynamo.MarshalMapOmitNull

// IsConditionFailed reports whether err represents a DynamoDB conditional
// check failure, either from a single-item call or from within a
// TransactWrite (TransactionCanceledException wrapping a condition failure).
var IsConditionFailed = dynamo.IsConditionFailed

// TableName returns the environment-prefixed physical table name
// ({prefix}_{table}). Exported for call sites outside the repository layer
// that need the physical name without a repository (e.g. the health probe).
func TableName(cfg *config.Config, table string) string {
	return dynamo.TableName(cfg.TablePrefix, table)
}

// NewBase creates a Base repository with an environment-prefixed table name.
func NewBase(db *dynamodb.Client, cfg *config.Config, table string) Base {
	return dynamo.NewBase(db, cfg.TablePrefix, table)
}

// Decode unmarshals DynamoDB attribute values into the target struct.
func Decode[T any](item map[string]types.AttributeValue) (*T, error) {
	return dynamo.Decode[T](item)
}

// Encode marshals a value into DynamoDB attribute values, omitting nulls.
func Encode(v any) (map[string]types.AttributeValue, error) {
	return dynamo.Encode(v)
}
