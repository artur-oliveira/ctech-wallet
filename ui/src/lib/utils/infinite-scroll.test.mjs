import assert from 'node:assert/strict'
import test from 'node:test'

import {INFINITE_SCROLL_ROOT_MARGIN, shouldLoadMore} from './infinite-scroll.ts'

const base = {isIntersecting: true, hasNextPage: true, isFetching: false, hasError: false}

test('loads the next page only when the sentinel is visible and more pages exist', () => {
  assert.equal(shouldLoadMore(base), true)
  assert.equal(shouldLoadMore({...base, isIntersecting: false}), false)
  assert.equal(shouldLoadMore({...base, hasNextPage: false}), false)
})

test('never loads while a request is in flight or after an error', () => {
  assert.equal(shouldLoadMore({...base, isFetching: true}), false)
  assert.equal(shouldLoadMore({...base, hasError: true}), false)
})

test('the margin prefetches before the user reaches the end', () => {
  assert.match(INFINITE_SCROLL_ROOT_MARGIN, /^\d+px 0px$/)
})
