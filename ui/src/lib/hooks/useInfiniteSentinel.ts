'use client'

import {useEffect, useState} from 'react'
import {INFINITE_SCROLL_ROOT_MARGIN, shouldLoadMore} from '@/lib/utils/infinite-scroll'

interface UseInfiniteSentinelOptions {
  hasNextPage: boolean
  isFetching: boolean
  hasError: boolean
  /** Rows currently shown. Changing it re-arms the observer, so a sentinel that
   *  is still in view after an append immediately asks for the next page. */
  itemCount: number
  fetchNextPage: () => void
}

/**
 * Returns a callback ref for a sentinel element placed after the last row.
 * When the sentinel nears the viewport the next page is fetched, until pages
 * run out or a fetch fails (then the caller shows a retry button).
 */
export function useInfiniteSentinel({
                                      hasNextPage,
                                      isFetching,
                                      hasError,
                                      itemCount,
                                      fetchNextPage,
                                    }: UseInfiniteSentinelOptions): (node: HTMLElement | null) => void {
  const [node, setNode] = useState<HTMLElement | null>(null)

  useEffect(() => {
    if (!node || !hasNextPage || hasError) return undefined
    const observer = new IntersectionObserver(
      (entries) => {
        if (shouldLoadMore({isIntersecting: !!entries[0]?.isIntersecting, hasNextPage, isFetching, hasError})) {
          fetchNextPage()
        }
      },
      {rootMargin: INFINITE_SCROLL_ROOT_MARGIN},
    )
    observer.observe(node)
    return () => observer.disconnect()
  }, [node, hasNextPage, hasError, isFetching, itemCount, fetchNextPage])

  return setNode
}
