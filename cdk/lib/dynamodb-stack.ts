import * as cdk from 'aws-cdk-lib';
import {RemovalPolicy} from 'aws-cdk-lib';
import * as dynamodb from 'aws-cdk-lib/aws-dynamodb';
import {Billing} from 'aws-cdk-lib/aws-dynamodb';
import {Construct} from 'constructs';
import {TABLE_HOLDS, TABLE_PIX_DEPOSITS} from './constants';
import {Environment} from './types';

/**
 * The wallet tables. Names/keys/indexes mirror api/tests/integration/setup_test.go
 * and internal/domain/wallet/model.go (GSIUser / GSIIdem / GSIStatus) exactly —
 * a mismatch here silently breaks every query at runtime.
 *
 * Naming: every table except `wallets` carries the `wallet_` segment
 * (`{env}_wallet_ledger_entries`, …) so they never collide with ctech-dfe's or
 * ctech-account's tables. The legacy pre-prefix tables are kept provisioned
 * during the migration (see LEGACY_TABLES) but receive no IAM access.
 */
export type TableName = (
  'wallets' |
  'wallet_audit' |
  'wallet_ledger_entries' |
  'wallet_idempotency' |
  typeof TABLE_PIX_DEPOSITS |
  'wallet_withdrawals' |
  'wallet_users' |
  typeof TABLE_HOLDS |
  'wallet_sandbox_purchases' |
  'wallet_product_purchases'
  );

/** Retired tables: kept provisioned (RETAIN) for data preservation only. */
type RetiredTableName =
  'wallet_baas_accounts' | 'wallet_transfer_intents' | 'wallet_settlement_legs' | 'wallet_med_receivables';

// GSI names — must match internal/domain/wallet/model.go.
const GSI_USER = 'gsi_user';
const GSI_IDEM = 'gsi_idem';
const GSI_STATUS = 'gsi_status';
const GSI_HOLD_STATUS = 'gsi_hold_status';
// Retired-table GSIs (see the retired BaaS block below).
const GSI_BAAS_ACCOUNT_ID = 'gsi_baas_account_id';
const GSI_BAAS_STATUS = 'gsi_baas_status';
const GSI_INTENT_STATUS = 'gsi_intent_status';
const GSI_BATCH_STATUS = 'gsi_batch_status';
const GSI_MED_STATUS = 'gsi_med_status';
const GSI_SANDBOX_PURCHASE_STATUS = 'gsi_sandbox_purchase_status';
const GSI_SANDBOX_PURCHASE_WEBHOOK_STATUS = 'gsi_sandbox_purchase_webhook_status';
const GSI_PRODUCT_PURCHASE_STATUS = 'gsi_product_purchase_status';
const GSI_PRODUCT_PURCHASE_WEBHOOK_STATUS = 'gsi_product_purchase_webhook_status';

// DynamoDB attribute names (single source of truth).
const ATTR_PK = 'pk';
const ATTR_SK = 'sk';
const ATTR_USER_ID = 'user_id';
const ATTR_IDEMPOTENCY_KEY = 'idempotency_key';
const ATTR_STATUS = 'status';
const ATTR_TTL = 'ttl';
const ATTR_PROVIDER_ACCOUNT_ID = 'provider_account_id';
const ATTR_WEBHOOK_STATUS = 'webhook_status';
const ATTR_CREATED_AT = 'created_at';

interface DynamoDBStackProps extends cdk.StackProps {
  tablePrefix: string;
  environment: Environment;
}

interface TableOptions {
  /** Add a sort key `sk` (only ledger_entries has one). */
  sortKey?: boolean;
  /** Enable DynamoDB TTL on the `ttl` attribute. */
  ttl?: boolean;
  /** Retired: force RETAIN and keep it out of `tables` (so it gets no IAM access). */
  retired?: boolean;
}

export class DynamoDBStack extends cdk.Stack {
  public readonly tables: Map<TableName, dynamodb.TableV2>;

  constructor(scope: Construct, id: string, props: DynamoDBStackProps) {
    super(scope, id, props);

    this.tables = new Map();
    const {tablePrefix, environment} = props;

    const removalPolicy = environment === 'dev' ? RemovalPolicy.DESTROY : RemovalPolicy.RETAIN;

    // PITR: ctech-dfe enables it on prod only, and this stack matches that so the
    // two services stay operationally identical. NOTE: this is a financial ledger —
    // if stage ever holds real money (real PIX credentials), PITR must be turned on
    // there too. Dev is sandbox-only, so prod-only is acceptable today.
    const pointInTimeRecoverySpecification =
      environment === 'prod' ? {pointInTimeRecoveryEnabled: true} : undefined;

    const table = (name: TableName | RetiredTableName, opts: TableOptions = {}): dynamodb.TableV2 => {
      const tableName = `${tablePrefix}_${name}`;
      const t = new dynamodb.TableV2(this, tableName, {
        tableName,
        partitionKey: {name: ATTR_PK, type: dynamodb.AttributeType.STRING},
        sortKey: opts.sortKey ? {name: ATTR_SK, type: dynamodb.AttributeType.STRING} : undefined,
        timeToLiveAttribute: opts.ttl ? ATTR_TTL : undefined,
        billing: Billing.onDemand({
          maxReadRequestUnits: 1000,
          maxWriteRequestUnits: 1000,
        }),
        removalPolicy: opts.retired ? RemovalPolicy.RETAIN : removalPolicy,
        pointInTimeRecoverySpecification,
        encryption: dynamodb.TableEncryptionV2.awsManagedKey(),
      });
      if (!opts.retired) this.tables.set(name as TableName, t);
      // The IAM/Reconcile stacks imported this ARN until now; keep the export
      // until they have deployed without it, or this stack's update fails with
      // "export in use".
      else this.exportValue(t.tableArn);
      return t;
    };

    const gsi = (t: dynamodb.TableV2, indexName: string, hashKey: string, sortKey?: string) => {
      t.addGlobalSecondaryIndex({
        indexName,
        partitionKey: {name: hashKey, type: dynamodb.AttributeType.STRING},
        sortKey: sortKey ? {name: sortKey, type: dynamodb.AttributeType.STRING} : undefined,
        projectionType: dynamodb.ProjectionType.ALL,
        warmThroughput: undefined,
        maxReadRequestUnits: 1000,
        maxWriteRequestUnits: 1000,
      });
    };

    // ── wallets: authoritative balance (atomic counter). pk = WALLET#{id} ──────
    const walletsTable = table('wallets');
    gsi(walletsTable, GSI_USER, ATTR_USER_ID); // both wallets of a user

    // ── wallet_ledger_entries: append-only audit trail. Never updated, never deleted
    const ledgerTable = table('wallet_ledger_entries', {sortKey: true});
    gsi(ledgerTable, GSI_IDEM, ATTR_IDEMPOTENCY_KEY); // replay lookup

    // ── wallet_idempotency: permanent IDEM#{key} guards ───────────────────────
    table('wallet_idempotency');

    // ── wallet_pix_deposits: durable charges keyed by txid ────────────────────
    // gsi_status backs reconciliation of pending deposits. Business expiration
    // closes charges but never deletes their idempotency/audit record.
    const depositsTable = table(TABLE_PIX_DEPOSITS);
    gsi(depositsTable, GSI_STATUS, ATTR_STATUS);

    // ── wallet_withdrawals: payouts; gsi_status drives the reconciliation job ──
    const withdrawalsTable = table('wallet_withdrawals');
    gsi(withdrawalsTable, GSI_STATUS, ATTR_STATUS);

    // ── wallet_users: per-user wallet metadata ────────────────────────────────
    const usersTable = table('wallet_users');

    // ── wallet_holds: open game-wallet reservations (skill-game integration,
    // e.g. ctech-poker buy-ins). gsi_hold_status drives the stale-hold
    // reconciliation sweep (24h ceiling, alarm-only — see reconcile.go).
    const holdsTable = table(TABLE_HOLDS);
    gsi(holdsTable, GSI_HOLD_STATUS, ATTR_STATUS);

    // ── wallet_audit: append-only record of actions that move NO money ─────────
    // consent, gambling activation, and every personal-limit change. The ledger
    // covers money; this covers everything else that must be provable after the
    // fact. Never updated, never deleted — same durability posture as the ledger,
    // because it is evidence. Already wallet_-prefixed, so unchanged.
    const auditTable = table('wallet_audit', {sortKey: true});

    // ── Retired BaaS custody tables ───────────────────────────────────────────
    // The BaaS custody integration was abandoned and all its code removed. These
    // tables stay declared ONLY so CloudFormation does not delete them: dev's
    // default policy is DESTROY, so dropping the constructs outright would delete
    // dev's tables. Forced to RETAIN, with no IAM access (not in `tables`). Next
    // step, after this has deployed to every env: delete this block — the tables
    // are then orphaned (kept) in the account for manual deletion. Keys/GSIs are
    // unchanged on purpose (no table update beyond the DeletionPolicy). The ARN
    // exports are kept for the same two-step reason (see table()).
    const baasAccountsTable = table('wallet_baas_accounts', {retired: true});
    gsi(baasAccountsTable, GSI_BAAS_ACCOUNT_ID, ATTR_PROVIDER_ACCOUNT_ID);
    gsi(baasAccountsTable, GSI_BAAS_STATUS, ATTR_STATUS);
    gsi(table('wallet_transfer_intents', {retired: true}), GSI_INTENT_STATUS, ATTR_STATUS);
    gsi(table('wallet_settlement_legs', {retired: true}), GSI_BATCH_STATUS, ATTR_STATUS);
    gsi(table('wallet_med_receivables', {retired: true}), GSI_MED_STATUS, ATTR_STATUS);

    // wallet_sandbox_purchases: pk = purchase_id, TTL for never-confirmed
    // purchases. Deliberately its own table, decoupled from wallet_pix_deposits:
    // a deposit is custody, this is a sale (plan §9.1/§9.3). gsi_sandbox_purchase_status
    // backs the pending-purchase sweep.
    const sandboxPurchasesTable = table('wallet_sandbox_purchases');
    gsi(sandboxPurchasesTable, GSI_SANDBOX_PURCHASE_STATUS, ATTR_STATUS);
    // Ownership-scoped, newest-first user purchase history. Reuses gsi_user's
    // established name; indexes are table-local and this one adds created_at
    // ordering without changing the wallets table's own hash-only index.
    gsi(sandboxPurchasesTable, GSI_USER, ATTR_USER_ID, ATTR_CREATED_AT);
    // Backs the M2M webhook notify-back retry sweep (RetryFailedM2MWebhooks) —
    // a purchase opened by an M2M client (e.g. ctech-poker) whose last
    // notify-back attempt failed. Empty/unset for user-direct purchases.
    gsi(sandboxPurchasesTable, GSI_SANDBOX_PURCHASE_WEBHOOK_STATUS, ATTR_WEBHOOK_STATUS);

    // wallet_product_purchases: pk = purchase_id — generic PIX product sale,
    // deliberately decoupled from every ledger table: no credit ever lands on
    // this purchase (docs/specs/2026-08-12-product-purchase-skus.md).
    // gsi_product_purchase_status backs the pending-purchase sweep.
    const productPurchasesTable = table('wallet_product_purchases');
    gsi(productPurchasesTable, GSI_PRODUCT_PURCHASE_STATUS, ATTR_STATUS);
    gsi(productPurchasesTable, GSI_USER, ATTR_USER_ID, ATTR_CREATED_AT);
    gsi(productPurchasesTable, GSI_PRODUCT_PURCHASE_WEBHOOK_STATUS, ATTR_WEBHOOK_STATUS);

    // ── Outputs ───────────────────────────────────────────────────────────────
    new cdk.CfnOutput(this, 'WalletsTableName', {
      value: walletsTable.tableName,
      exportName: `${id}-wallets-table`,
    });
    new cdk.CfnOutput(this, 'LedgerEntriesTableName', {
      value: ledgerTable.tableName,
      exportName: `${id}-ledger-entries-table`,
    });
    new cdk.CfnOutput(this, 'UsersTableName', {
      value: usersTable.tableName,
      exportName: `${id}-users-table`,
    });
    new cdk.CfnOutput(this, 'WalletAuditTableName', {
      value: auditTable.tableName,
      exportName: `${id}-wallet-audit-table`,
    });
  }
}
