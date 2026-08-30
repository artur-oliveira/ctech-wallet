import axios, {AxiosError, type AxiosInstance, type AxiosRequestConfig, type AxiosResponse,} from 'axios'
import type {
  Balances,
  DepositResult,
  GameLimits,
  GameLimitsInput,
  GameLimitsStatus,
  LedgerPage,
  MeResponse,
  OnboardingState,
  ProductPurchase,
  PurchasePage,
  SandboxPurchase,
  Transfer,
  Wallet,
  WalletType,
  Withdrawal,
} from '@/lib/types/api'
import {MockApiClient, USE_MOCK} from '@/lib/mock'
import {
  ApiUnavailableError,
  checkApiLiveness,
  HTTP_TIMEOUT_MS,
  requireApiLiveness,
} from '@/lib/network/liveness'
import {httpRetryDelay, isRetryableFailure} from '@/lib/network/retry'
import {SESSION_KEY_RETURN_AFTER_OUTAGE} from '@/lib/constants/storage'
import {UNAVAILABLE_PATH} from '@/lib/constants/routes'

// Empty means same-origin: CloudFront forwards /v1.0/* to the ALB in deployed
// environments, and `next dev` proxies it locally (next.config.ts). Either way
// the browser never makes a cross-origin request, so CORS never applies.
const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL ?? ''
const SANDBOX_PURCHASES_PATH = '/v1.0/wallet/sandbox/purchases'
const PRODUCT_PURCHASES_PATH = '/v1.0/wallet/product-purchases'

// Access token held in memory only — never written to localStorage.
let _accessToken: string | null = null

// AuthContext registers this to supply a fresh access token on 401.
let _refreshFn: (() => Promise<string | null>) | null = null

export function registerRefreshFn(fn: () => Promise<string | null>): void {
  _refreshFn = fn
}

export function getAccessToken(): string | null {
  return _accessToken
}

/**
 * Renews the in-memory access token out-of-band, for callers that are not an
 * HTTP request — today the WebSocket, whose auth frame is rejected after the
 * upgrade rather than as a 401 the interceptor could see.
 *
 * Returns null when no refresh is registered or the session is genuinely gone,
 * which the caller must treat as terminal.
 */
export async function refreshAccessToken(): Promise<string | null> {
  if (!_refreshFn) return null
  const token = await _refreshFn().catch(() => null)
  if (!token) return null
  _accessToken = token
  notifyTokenListeners(token)
  return token
}

const tokenListeners = new Set<(token: string) => void>()

export function subscribeAccessToken(cb: (token: string) => void): () => void {
  tokenListeners.add(cb)
  return () => tokenListeners.delete(cb)
}

function notifyTokenListeners(token: string): void {
  tokenListeners.forEach((cb) => cb(token))
}

interface ProblemBody {
  detail?: string
  title?: string
  type?: string

  [key: string]: unknown
}

async function parseErr(response: AxiosResponse): Promise<ProblemBody> {
  if (response.data instanceof Blob) {
    return JSON.parse(await response.data.text())
  }
  return response.data
}

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly detail: string,
    public readonly type?: string,
    public readonly raw?: unknown,
  ) {
    super(detail)
    this.name = 'ApiError'
  }
}

interface RetryableConfig extends AxiosRequestConfig {
  _retry?: boolean
  _networkRetryCount?: number
}

function wait(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

/**
 * A 503 is the API telling us it is not serving. Sending the user to the
 * maintenance page beats letting every query on the screen render its own error
 * toast for one shared cause. Where they were is remembered so the retry can
 * put them back rather than dumping them on the dashboard.
 */
export function redirectOnServiceUnavailable(status?: number): boolean {
  if (status !== 503 || typeof window === 'undefined' || window.location.pathname === UNAVAILABLE_PATH) return false
  try {
    window.sessionStorage.setItem(
      SESSION_KEY_RETURN_AFTER_OUTAGE,
      `${window.location.pathname}${window.location.search || ''}`,
    )
  } catch {
    // Storage is unavailable in some privacy modes; the dashboard remains the
    // safe fallback destination.
  }
  window.location.replace(UNAVAILABLE_PATH)
  return true
}

function createAxiosInstance(): AxiosInstance {
  const instance = axios.create({
    baseURL: API_BASE_URL,
    // Without this, a hung connection leaves the request pending forever and the
    // UI spins with nothing to cancel it.
    timeout: HTTP_TIMEOUT_MS,
    headers: {'Content-Type': 'application/json'},
  })

  instance.interceptors.request.use(async (config) => {
    // One health probe owns "is the API up", so a dead API fails these fast
    // instead of turning every mounted query into its own retry loop.
    if (!USE_MOCK) await requireApiLiveness()
    if (_accessToken) config.headers.Authorization = `Bearer ${_accessToken}`
    return config
  })

  instance.interceptors.response.use(
    (response) => response,
    async (error: AxiosError) => {
      const original = error.config as RetryableConfig | undefined
      if (error.response?.status === 401 && original && !original._retry && _refreshFn) {
        original._retry = true
        const newToken = await _refreshFn()
        if (newToken) {
          _accessToken = newToken
          notifyTokenListeners(newToken)
          original.headers = {...original.headers, Authorization: `Bearer ${newToken}`}
          return instance(original)
        }
        if (typeof window !== 'undefined') {
          const {startOAuthFlow} = await import('@/lib/auth/oauth')
          const returnTo = window.location.pathname === '/callback' ? '/' : window.location.pathname
          await startOAuthFlow(returnTo)
        }
        return
      }

      // Verify an ambiguous failure against liveness before retrying it: a
      // timeout during a real outage should stop, not retry twice more and then
      // report a generic error.
      let mayRetry = !(error instanceof ApiUnavailableError)
      if (!USE_MOCK && !error.response && mayRetry) mayRetry = await checkApiLiveness()
      if (mayRetry && original && isRetryableFailure({response: error.response, config: original})) {
        original._networkRetryCount = (original._networkRetryCount || 0) + 1
        const retryAfter = error.response?.headers?.['retry-after'] as string | undefined
        await wait(httpRetryDelay(original._networkRetryCount, retryAfter))
        return instance(original)
      }

      if (error instanceof ApiUnavailableError) {
        redirectOnServiceUnavailable(503)
        throw new ApiError(503, error.message, '/problems/service-unavailable', error)
      }
      redirectOnServiceUnavailable(error.response?.status)
      const data = error.response ? await parseErr(error.response) : undefined
      const detail = data?.detail ?? data?.title ?? error.message ?? `HTTP ${error.response?.status}`
      throw new ApiError(error.response?.status ?? 0, detail, data?.type, data)
    },
  )

  return instance
}

/** Options for mutating routes that require an Idempotency-Key header. */
function idemConfig(key: string): AxiosRequestConfig {
  return {headers: {'Idempotency-Key': key}}
}

class ApiClient {
  private readonly http: AxiosInstance

  constructor() {
    this.http = createAxiosInstance()
  }

  setToken(token: string | null): void {
    _accessToken = token
    if (token) notifyTokenListeners(token)
  }

  async me(): Promise<MeResponse> {
    return (await this.http.get<MeResponse>('/v1.0/auth/me')).data
  }

  async acceptTermsAddendum(): Promise<void> {
    await this.http.post('/v1.0/auth/terms-addendum/accept')
  }

  /**
   * Starts custody onboarding and returns the one-off verification fee to pay.
   * The payment account itself is only opened once that fee clears, because the
   * provider bills the moment it exists. Idempotent server-side: called again
   * while the fee is outstanding it returns the same charge, never a second one.
   * `incomeValue` is centavos, like every other amount on this client.
   */
  async initiateOnboarding(incomeValue: number): Promise<OnboardingState> {
    return (await this.http.post<OnboardingState>('/v1.0/wallet/onboarding', {income_value: incomeValue})).data
  }

  /** Current onboarding step. Read-only: opens no charge and no account. */
  async getOnboarding(): Promise<OnboardingState> {
    return (await this.http.get<OnboardingState>('/v1.0/wallet/onboarding')).data
  }

  async getBalances(): Promise<Balances> {
    return (await this.http.get<Balances>('/v1.0/wallet')).data
  }

  async createDeposit(amount: number, idempotencyKey: string): Promise<DepositResult> {
    return (
      await this.http.post<DepositResult>('/v1.0/wallet/deposits', {amount}, idemConfig(idempotencyKey))
    ).data
  }

  async createWithdrawal(amount: number, idempotencyKey: string): Promise<Withdrawal> {
    return (
      await this.http.post<Withdrawal>('/v1.0/wallet/withdrawals', {amount}, idemConfig(idempotencyKey))
    ).data
  }

  /** Buys sandbox credits with the GAME balance — never with the real balance. */
  async purchaseSandbox(amount: number, idempotencyKey: string): Promise<Transfer> {
    return (
      await this.http.post('/v1.0/wallet/sandbox/purchase', {amount}, idemConfig(idempotencyKey))
    ).data
  }

  /** Opts into the game + sandbox wallets. Requires verified KYC. */
  async activateGambling(limits: GameLimitsInput): Promise<{ game: Wallet; sandbox: Wallet }> {
    return (await this.http.post('/v1.0/wallet/gambling/activate', {
      accept_addendum: true,
      ...limits,
    })).data
  }

  async getGameLimits(): Promise<GameLimitsStatus> {
    return (await this.http.get<GameLimitsStatus>('/v1.0/wallet/gambling/limits')).data
  }

  async setGameLimits(limits: GameLimitsInput): Promise<GameLimits> {
    return (await this.http.put<GameLimits>('/v1.0/wallet/gambling/limits', limits)).data
  }

  async cancelPendingGameLimits(): Promise<GameLimits> {
    return (await this.http.delete<GameLimits>('/v1.0/wallet/gambling/limits/pending')).data
  }

  async selfExclude(period: '30d' | '90d' | 'indefinite'): Promise<void> {
    await this.http.post('/v1.0/wallet/gambling/self-exclude', {period})
  }

  async revokeSelfExclusion(): Promise<void> {
    await this.http.post('/v1.0/wallet/gambling/self-exclude/revoke')
  }

  /** real → game. The edge the user's personal limits are enforced on. */
  async fundGame(amount: number, idempotencyKey: string): Promise<Transfer> {
    return (
      await this.http.post('/v1.0/wallet/game/deposit', {amount}, idemConfig(idempotencyKey))
    ).data
  }

  /** game → real. Never limited, never charged — this is not a PIX payout. */
  async returnFromGame(amount: number, idempotencyKey: string): Promise<Transfer> {
    return (
      await this.http.post('/v1.0/wallet/game/withdraw', {amount}, idemConfig(idempotencyKey))
    ).data
  }

  async getLedger(type: WalletType, cursor?: string, limit = 50): Promise<LedgerPage> {
    const params = new URLSearchParams({limit: String(limit)})
    if (cursor) params.set('cursor', cursor)
    return (await this.http.get<LedgerPage>(`/v1.0/wallet/${type}/ledger?${params}`)).data
  }

  async getSandboxPurchases(cursor?: string, limit = 50): Promise<PurchasePage<SandboxPurchase>> {
    const params = new URLSearchParams({limit: String(limit)})
    if (cursor) params.set('cursor', cursor)
    return (await this.http.get<PurchasePage<SandboxPurchase>>(`${SANDBOX_PURCHASES_PATH}?${params}`)).data
  }

  async getProductPurchases(cursor?: string, limit = 50): Promise<PurchasePage<ProductPurchase>> {
    const params = new URLSearchParams({limit: String(limit)})
    if (cursor) params.set('cursor', cursor)
    return (await this.http.get<PurchasePage<ProductPurchase>>(`${PRODUCT_PURCHASES_PATH}?${params}`)).data
  }
}

export const apiClient = USE_MOCK
  ? (new MockApiClient() as unknown as ApiClient)
  : new ApiClient()
