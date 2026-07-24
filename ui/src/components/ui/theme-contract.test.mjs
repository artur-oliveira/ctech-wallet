import assert from 'node:assert/strict'
import {readdirSync, readFileSync} from 'node:fs'
import {fileURLToPath} from 'node:url'
import test from 'node:test'

const SOURCE_EXTENSIONS = ['.css', '.ts', '.tsx']

function readSources(directory) {
    return readdirSync(directory, {withFileTypes: true}).flatMap((entry) => {
        const path = `${directory}/${entry.name}`

        if (entry.isDirectory()) {
            return readSources(path)
        }

        return SOURCE_EXTENSIONS.some((extension) => entry.name.endsWith(extension))
            ? readFileSync(path, 'utf8')
            : []
    }).join('\n')
}

const globalsSource = readFileSync(
    new URL('../../app/globals.css', import.meta.url),
    'utf8',
)
const layoutSource = readFileSync(
    new URL('../../app/layout.tsx', import.meta.url),
    'utf8',
)
const appSources = readSources(fileURLToPath(new URL('../../', import.meta.url)))
const manifest = JSON.parse(
    readFileSync(new URL('../../../public/site.webmanifest', import.meta.url), 'utf8'),
)

test('the app declares one complete light-only theme contract', () => {
    assert.match(globalsSource, /color-scheme:\s*only light/)
    assert.doesNotMatch(
        appSources,
        /@custom-variant\s+dark|\.dark\s*\{|dark:/,
    )

    assert.match(layoutSource, /const LIGHT_THEME_COLOR = '#f8fafc'/)
    assert.match(layoutSource, /themeColor:\s*LIGHT_THEME_COLOR/)
    assert.match(layoutSource, /<Toaster[\s\S]*theme="light"/)
    assert.equal(manifest.theme_color, '#f8fafc')
    assert.equal(manifest.background_color, '#f8fafc')
})
