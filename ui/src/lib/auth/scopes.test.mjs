import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import {dirname, join} from 'node:path'
import {fileURLToPath} from 'node:url'
import test from 'node:test'

import {IDENTITY_SCOPES, OAUTH_SCOPE, WALLET_SCOPES} from './scopes.ts'

const manifestPath = join(
  dirname(fileURLToPath(import.meta.url)),
  '../../../../api/internal/oauthresource/scope-manifest.json',
)
const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'))

test('Wallet UI requests every public active scope from its Resource Server manifest', () => {
  const publicActive = manifest.scopes
    .filter((scope) => scope.visibility === 'public' && scope.status === 'active')
    .map((scope) => scope.name)
    .sort()

  assert.deepEqual([...WALLET_SCOPES].sort(), publicActive)
  assert.deepEqual(OAUTH_SCOPE.split(' '), [...IDENTITY_SCOPES, ...WALLET_SCOPES])
})

test('Wallet UI never requests internal service scopes', () => {
  assert.equal(OAUTH_SCOPE.split(' ').some((scope) => scope.startsWith('internal:')), false)
})
