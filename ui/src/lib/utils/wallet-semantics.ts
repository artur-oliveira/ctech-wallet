import type {WalletType} from '@/lib/types/api'

/** Real and game wallets both hold money; only sandbox holds non-monetary credits. */
export function walletHasMonetaryValue(type: WalletType): boolean {
    return type !== 'sandbox'
}
