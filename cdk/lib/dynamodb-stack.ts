import * as cdk from 'aws-cdk-lib';
import {RemovalPolicy} from 'aws-cdk-lib';
import * as dynamodb from 'aws-cdk-lib/aws-dynamodb';
import {Billing} from 'aws-cdk-lib/aws-dynamodb';
import {Construct} from 'constructs';
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
    'wallet_pix_deposits' |
    'wallet_withdrawals' |
    'wallet_users' |
    'wallet_holds' |
    // Asaas BaaS custody tables (docs/plans/2026-07-30-asaas-baas-implementation-plan.md
    // §2.4). Names are provider-neutral on purpose — Asaas is today's provider,
    // not a permanent commitment.
    'wallet_baas_accounts' |
    'wallet_transfer_intents' |
    'wallet_settlement_legs' |
    'wallet_med_receivables' |
    'wallet_sandbox_purchases'
    );

// GSI names — must match internal/domain/wallet/model.go.
const GSI_USER = 'gsi_user';
const GSI_IDEM = 'gsi_idem';
const GSI_STATUS = 'gsi_status';
const GSI_HOLD_STATUS = 'gsi_hold_status';
const GSI_DEPOSIT_PROVIDER_QR = 'gsi_deposit_provider_qr';
const GSI_BAAS_ACCOUNT_ID = 'gsi_baas_account_id';
const GSI_BAAS_STATUS = 'gsi_baas_status';
const GSI_INTENT_STATUS = 'gsi_intent_status';
const GSI_BATCH_STATUS = 'gsi_batch_status';
const GSI_MED_STATUS = 'gsi_med_status';
const GSI_SANDBOX_PURCHASE_STATUS = 'gsi_sandbox_purchase_status';
const GSI_SANDBOX_PURCHASE_WEBHOOK_STATUS = 'gsi_sandbox_purchase_webhook_status';

// DynamoDB attribute names (single source of truth).
const ATTR_PK = 'pk';
const ATTR_SK = 'sk';
const ATTR_USER_ID = 'user_id';
const ATTR_IDEMPOTENCY_KEY = 'idempotency_key';
const ATTR_STATUS = 'status';
const ATTR_TTL = 'ttl';
const ATTR_PROVIDER_ACCOUNT_ID = 'provider_account_id';
const ATTR_PROVIDER_QR_CODE_ID = 'provider_qr_code_id';
const ATTR_WEBHOOK_STATUS = 'webhook_status';

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
                    maxReadRequestUnits: 1000,
                    maxWriteRequestUnits: 1000,
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

        // ── wallet_idempotency: IDEM#{key} guard items, expire via TTL ─────────────
        table('wallet_idempotency', {ttl: true});

        // ── wallet_pix_deposits: in-flight charges keyed by txid, expire via TTL ───
        // gsi_status backs the pre-TTL sweep (F6): find pending deposits close to
        // expiry and re-query Inter once before the row is lost. gsi_deposit_provider_qr
        // resolves an Asaas payment webhook's pixQrCodeId back to the deposit it
        // belongs to (plan §4.3: "the webhook resolves payment.pixQrCodeId → txid,
        // not the other way round") — Asaas-opened deposits only, empty for every
        // Inter-opened row.
        const depositsTable = table('wallet_pix_deposits', {ttl: true});
        gsi(depositsTable, GSI_STATUS, ATTR_STATUS);
        gsi(depositsTable, GSI_DEPOSIT_PROVIDER_QR, ATTR_PROVIDER_QR_CODE_ID);

        // ── wallet_withdrawals: payouts; gsi_status drives the reconciliation job ──
        const withdrawalsTable = table('wallet_withdrawals');
        gsi(withdrawalsTable, GSI_STATUS, ATTR_STATUS);

        // ── wallet_users: per-user wallet metadata ────────────────────────────────
        const usersTable = table('wallet_users');

        // ── wallet_holds: open game-wallet reservations (skill-game integration,
        // e.g. ctech-poker buy-ins). gsi_hold_status drives the stale-hold
        // reconciliation sweep (24h ceiling, alarm-only — see reconcile.go).
        const holdsTable = table('wallet_holds');
        gsi(holdsTable, GSI_HOLD_STATUS, ATTR_STATUS);

        // ── wallet_audit: append-only record of actions that move NO money ─────────
        // consent, gambling activation, and every personal-limit change. The ledger
        // covers money; this covers everything else that must be provable after the
        // fact. Never updated, never deleted — same durability posture as the ledger,
        // because it is evidence. Already wallet_-prefixed, so unchanged.
        const auditTable = table('wallet_audit', {sortKey: true});

        // ── Asaas BaaS custody tables (implementation plan §2.4) ───────────────────
        // Deliberately their own tables, decoupled from the real/game/sandbox
        // ledger core, which "survives... none of it changes" (design spec §3.1).

        // wallet_baas_accounts: 1 row per user, pk = user_id. gsi_baas_account_id
        // resolves an Asaas account.id (from any webhook) back to the user;
        // gsi_baas_status backs the conservation-check sweep (plan §6) and the
        // account-status webhook's frozen/approved lookups.
        const baasAccountsTable = table('wallet_baas_accounts');
        gsi(baasAccountsTable, GSI_BAAS_ACCOUNT_ID, ATTR_PROVIDER_ACCOUNT_ID);
        gsi(baasAccountsTable, GSI_BAAS_STATUS, ATTR_STATUS);

        // wallet_transfer_intents: pk = external_reference — the transfer-
        // authorization webhook's single GetItem lookup (plan §2.3), and the
        // reconcile job's work queue via gsi_intent_status (awaiting_authorization/
        // processing).
        const transferIntentsTable = table('wallet_transfer_intents');
        gsi(transferIntentsTable, GSI_INTENT_STATUS, ATTR_STATUS);

        // wallet_settlement_legs: pk = batch_id (plan §6 netting batches). No
        // application code reads/writes this table yet — real-money multi-party
        // settlement (poker/dominó) has no caller anywhere in this codebase
        // (games gate not started, per plan §0/§10) — provisioned now for schema
        // completeness per the plan's own §2.4 table list, not because code needs
        // it today.
        const settlementLegsTable = table('wallet_settlement_legs');
        gsi(settlementLegsTable, GSI_BATCH_STATUS, ATTR_STATUS);

        // wallet_med_receivables: pk = receivable_id. A MED clawback shortfall
        // becomes a receivable here instead of a negative balance (Invariant #1
        // stays literal). gsi_med_status backs the open-debt scan that blocks
        // funding/withdrawal on the affected wallet (plan §7.3).
        const medReceivablesTable = table('wallet_med_receivables');
        gsi(medReceivablesTable, GSI_MED_STATUS, ATTR_STATUS);

        // wallet_sandbox_purchases: pk = purchase_id, TTL for never-confirmed
        // purchases. Deliberately its own table, decoupled from wallet_pix_deposits:
        // a deposit is custody, this is a sale (plan §9.1/§9.3). gsi_sandbox_purchase_status
        // backs the pending-purchase sweep.
        const sandboxPurchasesTable = table('wallet_sandbox_purchases', {ttl: true});
        gsi(sandboxPurchasesTable, GSI_SANDBOX_PURCHASE_STATUS, ATTR_STATUS);
        // Backs the M2M webhook notify-back retry sweep (RetryFailedM2MWebhooks) —
        // a purchase opened by an M2M client (e.g. ctech-poker) whose last
        // notify-back attempt failed. Empty/unset for user-direct purchases.
        gsi(sandboxPurchasesTable, GSI_SANDBOX_PURCHASE_WEBHOOK_STATUS, ATTR_WEBHOOK_STATUS);

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
