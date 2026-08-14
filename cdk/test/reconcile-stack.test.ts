import assert from 'node:assert/strict';
import {test} from 'node:test';
import {TABLE_HOLDS, TABLE_PIX_DEPOSITS} from '../lib/constants';
import {
  RECONCILE_DEPOSIT_TABLES,
  RECONCILE_HOLD_TABLES,
  tableAndIndexArns,
} from '../lib/reconcile-stack';

test('reconcile recovery grants include PIX deposit and stale-hold indexes', () => {
  const depositArn = `arn:aws:dynamodb:us-east-1:123456789012:table/prod_${TABLE_PIX_DEPOSITS}`;
  const tables = new Map([
    [TABLE_PIX_DEPOSITS, {tableArn: depositArn}],
    [TABLE_HOLDS, {tableArn: `arn:${TABLE_HOLDS}`}],
  ]);
  const depositResources = tableAndIndexArns(RECONCILE_DEPOSIT_TABLES, tables);
  const holdResources = tableAndIndexArns(RECONCILE_HOLD_TABLES, tables);

  assert.ok(depositResources.includes(depositArn));
  assert.ok(depositResources.includes(`${depositArn}/index/*`));
  assert.ok(holdResources.includes(`arn:${TABLE_HOLDS}/index/*`));
});
