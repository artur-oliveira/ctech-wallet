/**
 * When the realtime socket should stop reconnecting.
 *
 * The wallet's WS server accepts the upgrade and only THEN reads the auth frame,
 * rejecting a bad token with `{"type":"error","code":"unauthorized"}` before
 * closing. To the client library every one of those is a *successful* open, so
 * its backoff resets each time: an expired token is not a failing connection, it
 * is an endless series of working ones. A tab left in the background long enough
 * for the token to expire reconnects at full speed until it is closed.
 *
 * So `unauthorized` is terminal by default and recoverable exactly once — the
 * only thing that can genuinely fix it is a new token, and if a refresh cannot
 * produce one, more attempts cannot either. Realtime is a convenience on this
 * surface (every event it carries is also reachable by refetching), so going
 * quiet beats hammering the API with a credential already known to be dead.
 */

/** One renewal attempt per expired token. */
export const MAX_AUTH_RECOVERIES = 1

export type RealtimeAuthAction = 'ignore' | 'refresh' | 'stop'

export interface RealtimeAuthState {
  /** Renewals already attempted since the last genuinely new token. */
  recoveries: number
}

/**
 * Decides what an inbound frame means for the socket's auth lifecycle.
 * Deliberately pure: the reconnect-loop bug is a policy bug, and policy that
 * only exists inside a hook is policy that cannot be tested.
 */
export function realtimeAuthAction(
  message: { type?: string; code?: string } | null | undefined,
  state: RealtimeAuthState,
): RealtimeAuthAction {
  if (message?.type !== 'error' || message.code !== 'unauthorized') return 'ignore'
  return state.recoveries >= MAX_AUTH_RECOVERIES ? 'stop' : 'refresh'
}
