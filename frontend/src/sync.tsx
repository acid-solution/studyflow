import { createContext, ReactNode, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { API_URL } from './api'

export type SyncChange = { topics: string[]; resources: Array<{ type: string; id: string }>; reset: boolean }
type Listener = (change: SyncChange) => void
type Signal = { subscribe: (listener: Listener) => () => void; status: string }
const SyncContext = createContext<Signal | null>(null)
export const useSync = () => useContext(SyncContext)

export type SSEFrame = { event: string; id: string; data: string }
// Incremental line parser, including UTF-8 decoder chunks and CR/LF splits.
export class SSEParser {
  private line = ''
  private data: string[] = []
  private event = ''
  private id = ''
  private cr = false
  constructor(private receive: (frame: SSEFrame) => void) {}
  feed(text: string) {
    for (const character of text) {
      if (this.cr && character === '\n') { this.cr = false; continue }
      this.cr = character === '\r'
      if (character === '\r' || character === '\n') { this.consume(this.line); this.line = '' }
      else this.line += character
    }
  }
  private consume(line: string) {
    if (!line) {
      if (this.data.length) this.receive({ event: this.event || 'message', id: this.id, data: this.data.join('\n') })
      this.data = []; this.event = ''; this.id = ''; return
    }
    if (line.startsWith(':')) return
    const colon = line.indexOf(':'); const field = colon < 0 ? line : line.slice(0, colon)
    let value = colon < 0 ? '' : line.slice(colon + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    if (field === 'data') this.data.push(value)
    else if (field === 'event') this.event = value
    else if (field === 'id' && !value.includes('\0')) this.id = value
  }
}

function key(userID: string) { return `studyflow-sync:${API_URL}:${userID}` }
function storedCursor(userID: string) { try { return sessionStorage.getItem(key(userID)) || '' } catch { return '' } }
function saveCursor(userID: string, cursor: string) { try { sessionStorage.setItem(key(userID), cursor) } catch { /* storage can be unavailable */ } }
export function clearSyncCursor(userID: string) { try { sessionStorage.removeItem(key(userID)) } catch { /* no stored cursor */ } }

export function acceptFrame(frame: SSEFrame, cursor: string): { cursor: string; change: SyncChange | null } {
  if (frame.event === 'sync.ready') return { cursor, change: null }
  if (frame.event !== 'business.changed' && frame.event !== 'sync.reset') return { cursor, change: null }
  if (!/^(0|[1-9]\d*)$/.test(frame.id)) throw new Error('通知序号无效')
  const data = JSON.parse(frame.data) as { id?: string; cursor?: string; topics?: unknown; resources?: unknown }
  if (frame.event === 'sync.reset') {
    if (data.cursor !== frame.id) throw new Error('同步游标不匹配')
    return { cursor: frame.id, change: { topics: ['*'], resources: [], reset: true } }
  }
  if (data.id !== frame.id || !Array.isArray(data.topics) || !data.topics.every((topic) => typeof topic === 'string') || !Array.isArray(data.resources) || !data.resources.every((v) => v && typeof v.type === 'string' && typeof v.id === 'string')) throw new Error('通知内容无效')
  if (cursor && BigInt(frame.id) <= BigInt(cursor)) return { cursor, change: null }
  if (cursor && BigInt(frame.id) !== BigInt(cursor) + 1n) throw new Error('通知序号不连续')
  return { cursor: frame.id, change: { topics: data.topics as string[], resources: data.resources as SyncChange['resources'], reset: false } }
}

function delay(ms: number, signal: AbortSignal) {
  return new Promise<void>((resolve) => {
    if (signal.aborted) { resolve(); return }
    const finish = () => { clearTimeout(timer); signal.removeEventListener('abort', finish); resolve() }
    const timer = setTimeout(finish, ms); signal.addEventListener('abort', finish, { once: true })
  })
}

export function SyncProvider({ token, userID, onUnauthorized, children }: { token: string; userID: string; onUnauthorized: () => Promise<void>; children: ReactNode }) {
  const listeners = useRef(new Set<Listener>())
  const [status, setStatus] = useState('connecting')
  const subscribe = useCallback((listener: Listener) => { listeners.current.add(listener); return () => { listeners.current.delete(listener) } }, [])
  const unauthorized = useRef(onUnauthorized); unauthorized.current = onUnauthorized
  useEffect(() => () => clearSyncCursor(userID), [userID])
  useEffect(() => {
    const controller = new AbortController(); let cursor = storedCursor(userID); let pending: SyncChange | null = null; let timer: ReturnType<typeof setTimeout> | undefined
    if (cursor && !/^(0|[1-9]\d*)$/.test(cursor)) { cursor = ''; clearSyncCursor(userID) }
    const queue = (change: SyncChange) => {
      pending = { topics: [...new Set([...(pending?.topics ?? []), ...change.topics])], resources: [...(pending?.resources ?? []), ...change.resources], reset: Boolean(pending?.reset || change.reset) }
      if (!timer) timer = setTimeout(() => { const next = pending; pending = null; timer = undefined; if (next) listeners.current.forEach((listener) => listener(next)) }, 300)
    }
    // Returning to the page verifies current data independently of event replay.
    // Use the same query signal, so dirty editors keep their input and revision.
    const restore = () => {
      if (document.visibilityState === 'visible') queue({ topics: ['*'], resources: [], reset: false })
    }
    document.addEventListener('visibilitychange', restore)
    window.addEventListener('focus', restore)
    window.addEventListener('pageshow', restore)
    const run = async () => {
      let failures = 0
      while (!controller.signal.aborted) {
        try {
          setStatus('connecting')
          const headers: Record<string, string> = { Authorization: `Bearer ${token}`, Accept: 'text/event-stream' }
          if (cursor) headers['Last-Event-ID'] = cursor
          const response = await fetch(`${API_URL}/api/v1/events`, { headers, signal: controller.signal })
          if (response.status === 401) { await unauthorized.current(); return }
          if (!response.ok || !response.body || !response.headers.get('content-type')?.includes('text/event-stream')) throw new Error('同步连接暂不可用')
          const parser = new SSEParser((frame) => {
            const accepted = acceptFrame(frame, cursor)
            if (accepted.change) { queue(accepted.change); cursor = accepted.cursor; saveCursor(userID, cursor) }
            if (frame.event === 'sync.ready') { failures = 0; setStatus('connected') }
          })
          const reader = response.body.getReader(); const decoder = new TextDecoder()
          try {
            while (!controller.signal.aborted) { const chunk = await reader.read(); if (chunk.done) break; parser.feed(decoder.decode(chunk.value, { stream: true })) }
          } finally { await reader.cancel().catch(() => undefined); reader.releaseLock() }
          if (!controller.signal.aborted) throw new Error('同步连接已断开')
        } catch {
          if (controller.signal.aborted) return
          setStatus('offline'); failures++
          await delay(Math.min(30000, 1000 * 2 ** Math.min(5, failures - 1)) * (0.8 + Math.random() * 0.4), controller.signal)
        }
      }
    }
    void run()
    return () => {
      document.removeEventListener('visibilitychange', restore)
      window.removeEventListener('focus', restore)
      window.removeEventListener('pageshow', restore)
      controller.abort(); if (timer) clearTimeout(timer); if (pending) listeners.current.forEach((listener) => listener(pending!))
    }
  }, [token, userID])
  const signal = useMemo(() => ({ subscribe, status }), [subscribe, status])
  return <SyncContext.Provider value={signal}>{children}</SyncContext.Provider>
}

export function SyncStatus() {
  const sync = useSync()
  return sync?.status === 'offline' ? <div className="global-notice" role="status">连接暂时中断，恢复后会自动同步最新数据。</div> : null
}
