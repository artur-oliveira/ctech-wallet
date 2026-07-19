import type {MeResponse} from '@/lib/types/api'

export type ProtectedRouteState = 'loading' | 'lookup-error' | 'terms-required' | 'ready'

interface ProtectedRouteStateInput {
    authLoading: boolean
    authenticated: boolean
    profileLoading: boolean
    profile: MeResponse | undefined
}

export function resolveProtectedRouteState({
                                                authLoading,
                                                authenticated,
                                                profileLoading,
                                                profile,
                                            }: ProtectedRouteStateInput): ProtectedRouteState {
    if (authLoading || !authenticated || profileLoading) return 'loading'
    if (!profile) return 'lookup-error'
    return profile.terms_addendum_accepted ? 'ready' : 'terms-required'
}
