import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const dialogSource = await readFile(new URL('./dialog.tsx', import.meta.url), 'utf8')
const buttonSource = await readFile(new URL('./button.tsx', import.meta.url), 'utf8')
const layoutSource = await readFile(new URL('../../app/layout.tsx', import.meta.url), 'utf8')
const ledgerTabsSource = await readFile(new URL('../wallet/ledger-tabs.tsx', import.meta.url), 'utf8')

test('dialogs use a scrollable Base UI viewport instead of transform centering', () => {
    assert.match(dialogSource, /DialogPrimitive\.Viewport/)
    assert.match(dialogSource, /overflow-y-auto/)
    assert.doesNotMatch(dialogSource, /-translate-[xy]-1\/2/)
})

test('shared buttons expose 44px minimum targets to coarse pointers', () => {
    assert.match(buttonSource, /pointer:coarse/)
    assert.match(buttonSource, /min-h-11/)
    assert.match(buttonSource, /min-w-11/)
})

test('the viewport lets the virtual keyboard resize dialog layout', () => {
    assert.match(layoutSource, /interactiveWidget:\s*'resizes-content'/)
    assert.match(layoutSource, /viewportFit:\s*'cover'/)
})

test('ledger tabs expose complete keyboard and ARIA relationships', () => {
    assert.match(ledgerTabsSource, /role="tablist"/)
    assert.match(ledgerTabsSource, /role="tab"/)
    assert.match(ledgerTabsSource, /aria-selected=/)
    assert.match(ledgerTabsSource, /aria-controls=/)
    assert.match(ledgerTabsSource, /tabIndex=\{selectedTab === type \? 0 : -1\}/)
    assert.match(ledgerTabsSource, /role="tabpanel"/)
    assert.match(ledgerTabsSource, /aria-labelledby=/)
    assert.match(ledgerTabsSource, /onKeyDown=/)
})
