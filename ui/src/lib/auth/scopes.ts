export const IDENTITY_SCOPES = ['openid', 'profile', 'kyc'] as const

// Keep this list in exact sync with the public active entries in the Wallet
// Resource Server manifest. The first-party OAuth client must allow the same
// scopes; ctech-account clamps the authorization request to that grant.
export const WALLET_SCOPES = [
  'wallet:state:read',
  'wallet:terms:write',
  'wallet:balances:read',
  'wallet:ledger:read',
  'wallet:deposits:write',
  'wallet:withdrawals:write',
  'wallet:sandbox-purchases:read',
  'wallet:sandbox-purchases:write',
  'wallet:product-purchases:read',
  'wallet:game:write',
  'wallet:gambling:read',
  'wallet:gambling:write',
  'wallet:custody:write',
] as const

export const OAUTH_SCOPE = [...IDENTITY_SCOPES, ...WALLET_SCOPES].join(' ')
