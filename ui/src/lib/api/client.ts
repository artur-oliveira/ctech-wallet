import axios, {AxiosError, type AxiosInstance, type AxiosRequestConfig, type AxiosResponse,} from 'axios'
import type {
  Balances,
  DepositResult,
  GameLimits,
  GameLimitsInput,
  GameLimitsStatus,
  LedgerPage,
  MeResponse,
  Transfer,
  Wallet,
  WalletType,
  Withdrawal,
} from '@/lib/types/api'
import {MockApiClient, USE_MOCK} from '@/lib/mock'

// Empty means same-origin: CloudFront forwards /v1.0/* to the ALB in deployed
// environments, and `next dev` proxies it locally (next.config.ts). Either way
// the browser never makes a cross-origin request, so CORS never applies.
const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL ?? ''

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

function createAxiosInstance(): AxiosInstance {
  const instance = axios.create({
    baseURL: API_BASE_URL,
    headers: {'Content-Type': 'application/json'},
  })

  instance.interceptors.request.use((config) => {
    if (_accessToken) config.headers.Authorization = `Bearer ${_accessToken}`
    return config
  })

  instance.interceptors.response.use(
    (response) => response,
    async (error: AxiosError) => {
      const original = error.config as (AxiosRequestConfig & { _retry?: boolean }) | undefined
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
}

export const apiClient = USE_MOCK
  ? (new MockApiClient() as unknown as ApiClient)
  : new ApiClient()
