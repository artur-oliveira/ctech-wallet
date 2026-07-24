import assert from 'node:assert/strict'
import test from 'node:test'

import {resolveProtectedRouteState} from './protected-route-state.ts'

const acceptedProfile = {
    user_id: 'user_1',
    terms_addendum_accepted: true,
    terms_addendum_version: '1.0',
}

test('protected routes fail closed when the consent lookup returns no profile', () => {
    assert.equal(resolveProtectedRouteState({
        authLoading: false,
        authenticated: true,
        profileLoading: false,
        profile: undefined,
    }), 'lookup-error')
})

test('protected routes render content only after accepted consent is known', () => {
    assert.equal(resolveProtectedRouteState({
        authLoading: false,
        authenticated: true,
        profileLoading: false,
        profile: acceptedProfile,
    }), 'ready')

    assert.equal(resolveProtectedRouteState({
        authLoading: false,
        authenticated: true,
        profileLoading: false,
        profile: {...acceptedProfile, terms_addendum_accepted: false},
    }), 'terms-required')
})

test('protected routes remain blocked while authentication or consent is loading', () => {
    for (const state of [
        {authLoading: true, authenticated: false, profileLoading: false},
        {authLoading: false, authenticated: false, profileLoading: false},
        {authLoading: false, authenticated: true, profileLoading: true},
    ]) {
        assert.equal(resolveProtectedRouteState({...state, profile: undefined}), 'loading')
    }
})
