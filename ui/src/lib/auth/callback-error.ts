const CALLBACK_ERROR_KEY: Record<string, string> = {
    access_denied: 'accessDenied',
    login_required: 'sessionExpired',
    interaction_required: 'sessionExpired',
    consent_required: 'sessionExpired',
    temporarily_unavailable: 'unavailable',
    server_error: 'unavailable',
    invalid_request: 'invalidRequest',
    invalid_grant: 'invalidRequest',
}

export function callbackErrorKey(errorCode: string | null): string {
    if (!errorCode) return 'generic'
    return CALLBACK_ERROR_KEY[errorCode] ?? 'generic'
}
