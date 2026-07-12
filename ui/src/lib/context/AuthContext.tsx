'use client'

import {createContext, ReactNode, useCallback, useEffect, useRef, useState} from 'react'
import {apiClient, registerRefreshFn} from '@/lib/api/client'
import type {Profile} from '@/lib/types/api'
import {SESSION_KEY_REFRESH, STORAGE_KEY_USER} from '@/lib/constants/storage'
import {decodeIdToken, doRefresh, endSessionRedirect, revokeToken, startOAuthFlow} from '@/lib/auth/oauth'

interface AuthContextType {
  profile: Profile | null
  authenticated: boolean
  loading: boolean
  login: () => void
  logout: () => void
  handleCallback: (accessToken: string, refreshToken: string, idToken: string | null) => Promise<void>
}

export const AuthContext = createContext<AuthContextType | undefined>(undefined)

export function AuthProvider({children}: {children: ReactNode}) {
  const [profile, setProfile] = useState<Profile | null>(null)
  const [authenticated, setAuthenticated] = useState(false)
  const [loading, setLoading] = useState(true)
  const refreshTokenRef = useRef<string | null>(null)

  const persistToken = useCallback((accessToken: string, refreshToken: string) => {
    refreshTokenRef.current = refreshToken
    sessionStorage.setItem(SESSION_KEY_REFRESH, refreshToken)
    apiClient.setToken(accessToken)
    setAuthenticated(true)
  }, [])

  const tryRefresh = useCallback(async (): Promise<string | null> => {
    const rt = refreshTokenRef.current ?? sessionStorage.getItem(SESSION_KEY_REFRESH)
    if (!rt) return null
    const result = await doRefresh(rt)
    if (!result) {
      refreshTokenRef.current = null
      sessionStorage.removeItem(SESSION_KEY_REFRESH)
      setAuthenticated(false)
      return null
    }
    persistToken(result.accessToken, result.refreshToken)
    return result.accessToken
  }, [persistToken])

  useEffect(() => {
    registerRefreshFn(tryRefresh)
  }, [tryRefresh])

  const login = useCallback(() => {
    void startOAuthFlow(window.location.pathname)
  }, [])

  // Clearing local state is not a logout: the ctech_session SSO cookie survives
  // it, so /authorize would silently re-authenticate on the next login. The
  // revoke must land before we navigate away, hence the await.
  const logout = useCallback(() => {
    const rt = refreshTokenRef.current
    refreshTokenRef.current = null
    sessionStorage.removeItem(SESSION_KEY_REFRESH)
    localStorage.removeItem(STORAGE_KEY_USER)
    apiClient.setToken(null)
    setAuthenticated(false)
    setProfile(null)
    void (async () => {
      if (rt) await revokeToken(rt)
      endSessionRedirect()
    })()
  }, [])

  const handleCallback = useCallback(
    async (accessToken: string, refreshToken: string, idToken: string | null) => {
      persistToken(accessToken, refreshToken)
      const claims = idToken ? decodeIdToken(idToken) : null
      if (claims) {
        setProfile(claims)
        localStorage.setItem(STORAGE_KEY_USER, JSON.stringify(claims))
      }
    },
    [persistToken],
  )

  // On mount: attempt a silent refresh from the stored refresh token.
  useEffect(() => {
    void (async () => {
      const cached = localStorage.getItem(STORAGE_KEY_USER)
      if (cached) {
        try {
          setProfile(JSON.parse(cached) as Profile)
        } catch {
          localStorage.removeItem(STORAGE_KEY_USER)
        }
      }
      await tryRefresh()
      setLoading(false)
    })()
  }, [tryRefresh])

  return (
    <AuthContext.Provider value={{profile, authenticated, loading, login, logout, handleCallback}}>
      {children}
    </AuthContext.Provider>
  )
}
