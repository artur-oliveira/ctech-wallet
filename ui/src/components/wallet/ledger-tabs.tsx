'use client'

import {type KeyboardEvent, useRef, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {LedgerList} from '@/components/wallet/ledger-list'
import type {WalletType} from '@/lib/types/api'
import {nextLedgerTab} from '@/lib/utils/ledger-tabs'

const TAB_ID_PREFIX = 'ledger-tab-'
const PANEL_ID_PREFIX = 'ledger-panel-'

function ledgerTabs(activated: boolean): WalletType[] {
  return activated ? ['real', 'game', 'sandbox'] : ['real']
}

function tabID(type: WalletType): string {
  return `${TAB_ID_PREFIX}${type}`
}

function panelID(type: WalletType): string {
  return `${PANEL_ID_PREFIX}${type}`
}

export function LedgerTabs({activated}: { activated: boolean }) {
  const {t} = useTranslation()
  const [tab, setTab] = useState<WalletType>('real')
  const tabRefs = useRef<Partial<Record<WalletType, HTMLButtonElement | null>>>({})
  const tabs = ledgerTabs(activated)
  const selectedTab = tabs.includes(tab) ? tab : tabs[0]

  function handleKeyDown(event: KeyboardEvent<HTMLButtonElement>, current: WalletType) {
    const next = nextLedgerTab(tabs, current, event.key)
    if (!next) return

    event.preventDefault()
    setTab(next)
    tabRefs.current[next]?.focus()
  }

  return (
    <section className="overflow-hidden rounded-xl border border-border bg-card">
      <div
        role="tablist"
        aria-label={t('dashboard.ledger.label')}
        aria-orientation="horizontal"
        className="flex overflow-x-auto border-b border-border"
      >
        {tabs.map((type) => (
          <button
            key={type}
            ref={(element) => {
              tabRefs.current[type] = element
            }}
            id={tabID(type)}
            type="button"
            role="tab"
            aria-selected={selectedTab === type}
            aria-controls={panelID(type)}
            tabIndex={selectedTab === type ? 0 : -1}
            onClick={() => setTab(type)}
            onKeyDown={(event) => handleKeyDown(event, type)}
            className={`px-5 py-3.5 text-xs font-semibold uppercase tracking-wider transition-colors whitespace-nowrap focus-visible:outline-offset-[-3px] [@media(pointer:coarse)]:min-h-11 [@media(pointer:coarse)]:min-w-11 ${
              selectedTab === type
                ? 'border-b-2 border-brand-600 text-brand-700'
                : 'text-muted-foreground hover:text-foreground'
            }`}
          >
            {t(`dashboard.ledger.tab.${type}`)}
          </button>
        ))}
      </div>

      {tabs.map((type) => (
        <div
          key={type}
          id={panelID(type)}
          role="tabpanel"
          aria-labelledby={tabID(type)}
          tabIndex={0}
          hidden={selectedTab !== type}
        >
          {selectedTab === type && <LedgerList type={type}/>}
        </div>
      ))}
    </section>
  )
}
