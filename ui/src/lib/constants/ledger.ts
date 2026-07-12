/** Ledger entry types as returned by the API (api/internal/domain/wallet/model.go). */
export const ENTRY_LABELS: Record<string, string> = {
  deposit: 'Depósito',
  withdraw: 'Saque',
  fee: 'Taxa de saque',
  game_debit: 'Aposta',
  game_credit: 'Prêmio',
  sandbox_purchase: 'Compra de créditos',
  sandbox_credit: 'Créditos recebidos',
  reversal: 'Estorno de saque',
}

export function entryLabel(type: string): string {
  return ENTRY_LABELS[type] ?? type
}
