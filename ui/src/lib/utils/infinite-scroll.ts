/** Prefetch the next page this far before the sentinel enters the viewport. */
export const INFINITE_SCROLL_ROOT_MARGIN = '400px 0px'

interface LoadMoreState {
  isIntersecting: boolean
  hasNextPage: boolean
  isFetching: boolean
  /** A failed page fetch stops the auto-loading; the user retries explicitly. */
  hasError: boolean
}

/** Pure decision behind the sentinel observer, kept separate so it is testable. */
export function shouldLoadMore(s: LoadMoreState): boolean {
  return s.isIntersecting && s.hasNextPage && !s.isFetching && !s.hasError
}
