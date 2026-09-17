import { useCallback, useEffect, useState } from 'react'
import type { PageResult } from './api'

export const PAGE_SIZE = 12

// Each request owns its result. Cleanup protects against clients that ignore abort.
export function usePagedList<T>(load: (pageNo: number, pageSize: number, signal: AbortSignal) => Promise<PageResult<T>>) {
  const [request, setRequest] = useState({ pageNo: 1, pageSize: PAGE_SIZE, revision: 0 })
  const [result, setResult] = useState<{
    request: typeof request
    data?: PageResult<T>
    error?: string
  } | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    let current = true
    void load(request.pageNo, request.pageSize, controller.signal).then(
      (data) => { if (current) setResult({ request, data }) },
      (error: unknown) => {
        if (current) setResult({ request, error: error instanceof Error ? error.message : '网络异常，请重试' })
      },
    )
    return () => {
      current = false
      controller.abort()
    }
  }, [load, request])

  const current = result?.request === request ? result : null
  const data = current?.data
  const pageNo = data?.pageNo ?? request.pageNo
  const pageSize = data?.pageSize ?? request.pageSize
  const refresh = useCallback(() => setRequest((value) => ({ ...value, revision: value.revision + 1 })), [])
  const goTo = (page: number) => setRequest((value) => ({ ...value, pageNo: Math.max(1, page), pageSize }))

  return {
    data: data?.data ?? [],
    total: data?.total,
    pageNo,
    pageSize,
    loading: current === null,
    error: current?.error,
    // No invented total: a full page permits one more probe; a short/empty page stops.
    hasNext: !!data && (data.total === undefined ? data.data.length >= pageSize : pageNo * pageSize < data.total),
    refresh,
    goTo,
  }
}
