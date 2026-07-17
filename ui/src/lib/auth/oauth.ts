import { OAuthClient, decodeIdToken as sdkDecodeIdToken } from '@aoctech/auth-client'
import type { IdTokenClaims } from '@aoctech/auth-client'

const CTECH_URL = process.env.NEXT_PUBLIC_CTECH_URL!
const CLIENT_ID = process.env.NEXT_PUBLIC_CTECH_CLIENT_ID!

const client = new OAuthClient({
  baseUrl: CTECH_URL,
  clientId: CLIENT_ID,
  redirectUri: typeof window !== 'undefined' ? `${window.location.origin}/callback` : '',
  scope: 'openid profile kyc',
})

export type { IdTokenClaims }

/**
 * Decodes the name-related profile claims from an OIDC id_token.
 *
 * The id_token's audience is the OAuth client itself, so reading profile data from it
 * client-side avoids the ctech-account /userinfo audience block on the wallet access token.
 */
export const decodeIdToken = sdkDecodeIdToken

export async function startOAuthFlow(returnTo = '/'): Promise<void> {
  await client.startOAuthFlow(returnTo)
}

/**
 * Step-up variant of startOAuthFlow: forces ctech-account to require a fresh
 * interactive login (max_age=0) instead of silently re-authenticating from
 * the existing SSO session — a valid session cookie alone never re-proves
 * MFA, which is exactly what a withdrawal's step-up check needs.
 */
export async function startStepUpFlow(returnTo = '/'): Promise<void> {
  await client.startOAuthFlow(returnTo, {maxAge: 0})
}

export async function exchangeCode(
  code: string,
  state: string,
): Promise<{ accessToken: string; idToken: string | null; returnTo: string }> {
  const result = await client.exchangeCode(code, state)
  return { accessToken: result.accessToken, idToken: result.idToken ?? null, returnTo: result.returnTo }
}

// M2: refresh_token is no longer passed in the request body — ctech-account
// issues it as an HttpOnly ctech_rt cookie the browser sends automatically via
// credentials:'include'. The SPA only ever sees the short-lived access token.
//
// Guarded + single-flight (ctech-oauth-client): this used to fire unconditionally
// on every AuthProvider mount, including a browser that never had a session —
// a guaranteed /v1.0/token failure that counted against ctech-account's shared
// brute-force rate limit on every cold visit. It now skips the request entirely
// without the ctech_auth hint cookie, or after a local revoked mark.
export async function doRefresh(): Promise<{ accessToken: string } | null> {
  const result = await client.refresh()
  return result ? { accessToken: result.accessToken } : null
}

/**
 * Redirects the browser to ctech-account's RP-Initiated-Logout endpoint,
 * ending the SSO session cookie (ctech_session) — not just this app's local
 * tokens. Without this, ctech_session stays valid and /authorize silently
 * re-authenticates on the very next login attempt, bouncing the user straight
 * back in instead of showing a fresh login.
 */
export function endSessionRedirect(returnTo = '/login'): void {
  client.endSessionRedirect(returnTo)
}

// M2: the refresh token lives in the HttpOnly ctech_rt cookie; we don't have it
// in JS. credentials:'include' sends the cookie and ctech-account's /revoke
// clears it, ending the refresh chain.
export async function revokeToken(): Promise<void> {
  await client.revoke()
}