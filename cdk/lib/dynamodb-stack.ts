import * as cdk from 'aws-cdk-lib';
import {RemovalPolicy} from 'aws-cdk-lib';
import * as dynamodb from 'aws-cdk-lib/aws-dynamodb';
import {Billing} from 'aws-cdk-lib/aws-dynamodb';
import {Construct} from 'constructs';
import {Environment} from './types';

/**
 * The six wallet tables. Names/keys/indexes mirror api/tests/integration/setup_test.go
 * and internal/domain/wallet/model.go (GSIUser / GSIIdem / GSIStatus) exactly —
 * a mismatch here silently breaks every query at runtime.
 */
export type TableName = (
  'wallets' |
  'ledger_entries' |
  'idempotency' |
  'pix_deposits' |
  'withdrawals' |
  'users' |
  'wallet_audit'
  );

// GSI names — must match internal/domain/wallet/model.go.
const GSI_USER = 'gsi_user';
const GSI_IDEM = 'gsi_idem';
const GSI_STATUS = 'gsi_status';

// DynamoDB attribute names (single source of truth).
const ATTR_PK = 'pk';
const ATTR_SK = 'sk';
const ATTR_USER_ID = 'user_id';
const ATTR_IDEMPOTENCY_KEY = 'idempotency_key';
const ATTR_STATUS = 'status';
const ATTR_TTL = 'ttl';

interface DynamoDBStackProps extends cdk.StackProps {
  tablePrefix: string;
  environment: Environment;
}

interface TableOptions {
  /** Add a sort key `sk` (only ledger_entries has one). */
  sortKey?: boolean;
  /** Enable DynamoDB TTL on the `ttl` attribute. */
  ttl?: boolean;
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

    const table = (name: TableName, opts: TableOptions = {}): dynamodb.TableV2 => {
      const tableName = `${tablePrefix}_${name}`;
      const t = new dynamodb.TableV2(this, tableName, {
        tableName,
        partitionKey: {name: ATTR_PK, type: dynamodb.AttributeType.STRING},
        sortKey: opts.sortKey ? {name: ATTR_SK, type: dynamodb.AttributeType.STRING} : undefined,
        timeToLiveAttribute: opts.ttl ? ATTR_TTL : undefined,
        billing: Billing.onDemand({
          maxReadRequestUnits: 5,
          maxWriteRequestUnits: 5,
        }),
        removalPolicy,
        pointInTimeRecoverySpecification,
        encryption: dynamodb.TableEncryptionV2.awsManagedKey(),
      });
      this.tables.set(name, t);
      return t;
    };

    const gsi = (t: dynamodb.TableV2, indexName: string, hashKey: string) => {
      t.addGlobalSecondaryIndex({
        indexName,
        partitionKey: {name: hashKey, type: dynamodb.AttributeType.STRING},
        projectionType: dynamodb.ProjectionType.ALL,
        warmThroughput: undefined,
        maxReadRequestUnits: 10,
        maxWriteRequestUnits: 10,
      });
    };

    // ── wallets: authoritative balance (atomic counter). pk = WALLET#{id} ──────
    const walletsTable = table('wallets');
    gsi(walletsTable, GSI_USER, ATTR_USER_ID); // both wallets of a user

    // ── ledger_entries: append-only audit trail. Never updated, never deleted ──
    const ledgerTable = table('ledger_entries', {sortKey: true});
    gsi(ledgerTable, GSI_IDEM, ATTR_IDEMPOTENCY_KEY); // replay lookup

    // ── idempotency: IDEM#{key} guard items, expire via TTL ───────────────────
    table('idempotency', {ttl: true});

    // ── pix_deposits: in-flight charges keyed by txid, expire via TTL ─────────
    table('pix_deposits', {ttl: true});

    // ── withdrawals: payouts; gsi_status drives the reconciliation job ────────
    const withdrawalsTable = table('withdrawals');
    gsi(withdrawalsTable, GSI_STATUS, ATTR_STATUS);

    // ── users: per-user wallet metadata ───────────────────────────────────────
    const usersTable = table('users');

    // ── wallet_audit: append-only record of actions that move NO money —────────
    // consent, gambling activation, and every personal-limit change. The ledger
    // covers money; this covers everything else that must be provable after the
    // fact. Never updated, never deleted — same durability posture as the ledger,
    // because it is evidence.
    const auditTable = table('wallet_audit', {sortKey: true});

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
