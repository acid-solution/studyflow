import { useCallback, useEffect, useRef, useState } from 'react'
import { useSync } from './sync'
import { APIError } from './api'

export function useLoad<T>(loader: () => Promise<T>, deps: unknown[], topics: string[] = ['*']) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState('')
  const [failure, setFailure] = useState<unknown>(null)
  const [loading, setLoading] = useState(true)
  const request = useRef(0); const alive = useRef(true)
  const retry = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const retryCount = useRef(0)
  const sync = useSync()
  const reload = useCallback(async (quiet = false, retryOnFailure = false): Promise<T | undefined> => {
    const number = ++request.current
    if (retry.current) clearTimeout(retry.current)
    if (!quiet) setLoading(true)
    setError(''); setFailure(null)
    try {
      const value = await loader()
      if (alive.current && number === request.current) { setData(value); retryCount.current = 0 }
      return value
    } catch (reason) {
      if (alive.current && number === request.current) {
        setError(reason instanceof Error ? reason.message : '操作失败，请稍后重试'); setFailure(reason)
        if (retryOnFailure && !(reason instanceof APIError && reason.status >= 400 && reason.status < 500)) { retryCount.current++; retry.current = setTimeout(() => { void reload(true, true) }, Math.min(30000, 1000 * 2 ** Math.min(5, retryCount.current - 1))) }
      }
      return undefined
    } finally { if (alive.current && number === request.current) setLoading(false) }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
  useEffect(() => { alive.current = true; void reload(); return () => { alive.current = false; request.current++; if (retry.current) clearTimeout(retry.current) } }, [reload])
  const topicKey = topics.join('|')
  useEffect(() => sync?.subscribe((change) => { if (change.reset || change.topics.includes('*') || topicKey === '*' || change.topics.some((topic) => topicKey.split('|').includes(topic))) void reload(true, true) }), [sync?.subscribe, reload, topicKey])
  return { data, error, failure, loading, reload }
}
