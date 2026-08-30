'use client'

import {useEffect} from 'react'

/**
 * Last-resort boundary, for a failure in the root layout or the provider tree.
 *
 * It renders its own <html>/<body> because at this point Next has torn the root
 * layout down. That also means i18n, fonts, the query client and the design
 * tokens are all unavailable — so the copy is hardcoded pt-BR (the primary
 * locale) and the styling is inline. Reaching for the shared SystemState here
 * would import the very tree that just failed.
 *
 * Route-level errors are handled by error.tsx, which keeps the shell alive.
 */
export default function GlobalError({error, reset}: { error: Error & { digest?: string }; reset: () => void }) {
  useEffect(() => {
    console.error(error)
  }, [error])

  return (
    <html lang="pt-BR">
    <body style={{margin: 0, background: '#f8fafc', color: '#0f172a', fontFamily: 'ui-sans-serif, system-ui, sans-serif'}}>
    <main style={{minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '4rem 1.5rem'}}>
      <div style={{width: '100%', maxWidth: '28rem'}}>
        <p style={{margin: 0, fontFamily: 'ui-monospace, monospace', fontSize: '3.75rem', lineHeight: 1, fontWeight: 600, color: 'rgba(15,23,42,0.12)'}}>
          500
        </p>
        <p style={{margin: '1.25rem 0 0', fontFamily: 'ui-monospace, monospace', fontSize: '0.75rem', fontWeight: 600, letterSpacing: '0.08em', textTransform: 'uppercase', color: '#64748b'}}>
          Falha no aplicativo
        </p>
        <h1 style={{margin: '0.5rem 0 0', fontSize: '1.25rem', fontWeight: 600}}>
          A carteira não conseguiu iniciar.
        </h1>
        <p style={{margin: '0.5rem 0 0', fontSize: '0.875rem', color: '#64748b'}}>
          O aplicativo foi interrompido antes de carregar. Nada foi enviado ao banco.
        </p>
        <p style={{margin: '0.75rem 0 0', fontSize: '0.875rem', color: '#64748b'}}>
          {error.digest ? `Referência do erro: ${error.digest}` : 'Tente iniciar novamente.'}
        </p>
        <button
          onClick={reset}
          style={{marginTop: '1.75rem', height: '2.25rem', padding: '0 0.75rem', border: 0, borderRadius: '0.625rem', background: '#7c3aed', color: '#fff', fontSize: '0.875rem', fontWeight: 500, cursor: 'pointer'}}
        >
          Tentar novamente
        </button>
        <p style={{margin: '2rem 0 0', paddingTop: '1rem', borderTop: '1px solid #e2e8f0', fontSize: '0.75rem', color: '#64748b'}}>
          Seu saldo e seu histórico não foram afetados.
        </p>
      </div>
    </main>
    </body>
    </html>
  )
}
