import axios, {AxiosError, type AxiosInstance, type AxiosRequestConfig, type AxiosResponse,} from 'axios'
import type {
  Balances,
  DepositResult,
  LedgerEntry,
  LedgerPage,
  Page,
  MeResponse,
  ProductPurchase,
  PurchasePage,
  SandboxPurchase,
  WalletType,
  Withdrawal,
} from '@/lib/types/api'
import {MockApiClient, USE_MOCK} from '@/lib/mock'
import {withRetry} from '@aoctech/ui'
import {ApiUnavailableError, assertApiReachable, classifyApiFailure} from '@/lib/network/api-reachability'
import {SESSION_KEY_RETURN_AFTER_OUTAGE} from '@/lib/constants/storage'
import {UNAVAILABLE_PATH} from '@/lib/constants/routes'

/**
 * 10s covers a cold Lambda behind pix-gateway and a slow mobile network, and
 * still fails inside a user's patience. Longer and the UI lies about being
 * busy; shorter and a legitimate PIX charge round-trip gets cancelled mid-flight.
 */
const HTTP_TIMEOUT_MS = 10_000

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
    public readonly retryAfter?: string,
  ) {
    super(detail)
    this.name = 'ApiError'
  }
}

interface RetryableConfig extends AxiosRequestConfig {
  _retry?: boolean
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

      // Retries live in ApiClient.call (withRetry); here the failure is only parsed.
      const data = error.response ? await parseErr(error.response) : undefined
      const detail = data?.detail ?? data?.title ?? error.message ?? `HTTP ${error.response?.status}`
      throw new ApiError(
        error.response?.status ?? 0,
        detail,
        data?.type,
        data,
        error.response?.headers?.['retry-after'] as string | undefined,
      )
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

  /**
   * One health probe (NetworkProvider) owns "is the API up", so a dead API fails
   * fast here. Retry is withRetry's: safe methods, or a mutation carrying an
   * Idempotency-Key (money safety), max 2, jittered, honoring Retry-After.
   * A 503 is redirected to /unavailable only once retries are exhausted.
   */
  private async call<T>(config: AxiosRequestConfig): Promise<T> {
    let response
    try {
      response = await withRetry(
        () => {
          if (!USE_MOCK) assertApiReachable()
          return this.http.request<T>(config)
        },
        {
          method: config.method,
          idempotent: Boolean(config.headers?.['Idempotency-Key']),
          classify: classifyApiFailure,
        },
      )
    } catch (error) {
      if (error instanceof ApiUnavailableError) {
        redirectOnServiceUnavailable(503)
        throw new ApiError(503, error.message, '/problems/service-unavailable', error)
      }
      if (error instanceof ApiError) redirectOnServiceUnavailable(error.status)
      throw error
    }
    return response.data
  }

  async me(): Promise<MeResponse> {
    return this.call({method: 'get', url: '/v1.0/auth/me'})
  }

  async acceptTermsAddendum(): Promise<void> {
    await this.call({method: 'post', url: '/v1.0/auth/terms-addendum/accept'})
  }

  async getBalances(): Promise<Balances> {
    return this.call({method: 'get', url: '/v1.0/wallet'})
  }

  async createDeposit(amount: number, idempotencyKey: string): Promise<DepositResult> {
    return this.call({method: 'post', url: '/v1.0/wallet/deposits', data: {amount}, ...idemConfig(idempotencyKey)})
  }

  async createWithdrawal(amount: number, idempotencyKey: string): Promise<Withdrawal> {
    return this.call({method: 'post', url: '/v1.0/wallet/withdrawals', data: {amount}, ...idemConfig(idempotencyKey)})
  }

  async getLedger(type: WalletType, cursor?: string, limit = 50): Promise<LedgerPage> {
    const params = new URLSearchParams({limit: String(limit)})
    if (cursor) params.set('cursor', cursor)
    return asPage(await this.call<RawPage<LedgerEntry>>({method: 'get', url: `/v1.0/wallet/${type}/ledger?${params}`}))
  }

  async getSandboxPurchases(cursor?: string, limit = 50): Promise<PurchasePage<SandboxPurchase>> {
    const params = new URLSearchParams({limit: String(limit)})
    if (cursor) params.set('cursor', cursor)
    return asPage(await this.call<RawPage<SandboxPurchase>>({method: 'get', url: `${SANDBOX_PURCHASES_PATH}?${params}`}))
  }

  async getProductPurchases(cursor?: string, limit = 50): Promise<PurchasePage<ProductPurchase>> {
    const params = new URLSearchParams({limit: String(limit)})
    if (cursor) params.set('cursor', cursor)
    return asPage(await this.call<RawPage<ProductPurchase>>({method: 'get', url: `${PRODUCT_PURCHASES_PATH}?${params}`}))
  }
}

/**
 * Pages are `{data, next_cursor}`. The previous API answered `{items, next_cursor, has_next}`; accept
 * both while the two deploy separately, so neither order of rollout breaks a list.
 */
type RawPage<T> = { data?: T[]; items?: T[]; next_cursor?: string | null }

function asPage<T>(raw: RawPage<T>): Page<T> {
  return {data: raw.data ?? raw.items ?? [], next_cursor: raw.next_cursor ?? null}
}

export const apiClient = USE_MOCK
  ? (new MockApiClient() as unknown as ApiClient)
  : new ApiClient()
