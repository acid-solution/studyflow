import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { APIError, api, API_URL } from './api'
import { acceptFrame, SSEParser, SyncProvider, SyncStatus } from './sync'
import { useLoad } from './data'
import TaskDrawer from './TaskDrawer'
import type { Task } from './types'

vi.mock('./api', async () => ({ ...(await vi.importActual<typeof import('./api')>('./api')), api: vi.fn() }))
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); sessionStorage.clear() })

function event(id: string, topics = ['tasks']) {
  return { event: 'business.changed', id, data: JSON.stringify({ id, kind: 'task.updated', topics, resources: [{ type: 'task', id: 'task-1' }] }) }
}
function frameText(value: { event: string; id: string; data: string }) { return `id: ${value.id}\nevent: ${value.event}\ndata: ${value.data}\n\n` }
function streamMock() {
  const streams: Array<{ controller: ReadableStreamDefaultController<Uint8Array>; signal: AbortSignal; headers: Headers }> = []
  vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) => {
    let controller!: ReadableStreamDefaultController<Uint8Array>
    const stream = new ReadableStream<Uint8Array>({ start(value) { controller = value } })
    const signal = init.signal as AbortSignal
    signal.addEventListener('abort', () => { try { controller.error(new DOMException('aborted', 'AbortError')) } catch { /* already canceled */ } }, { once: true })
    streams.push({ controller, signal, headers: new Headers(init.headers) })
    return new Response(stream, { headers: { 'Content-Type': 'text/event-stream' } })
  }))
  return { streams, send: (text: string, index = 0) => streams[index].controller.enqueue(new TextEncoder().encode(text)) }
}

describe('notification stream protocol', () => {
  it('parses CRLF across chunks and combines data lines while ignoring heartbeats', () => {
    const received: unknown[] = []; const parser = new SSEParser((frame) => received.push(frame))
    for (const character of ': heartbeat\r\n\r\nid: 12\r\nevent: business.changed\r\ndata: 中文\r\ndata: 第二行\r\n\r\n') parser.feed(character)
    expect(received).toEqual([{ id: '12', event: 'business.changed', data: '中文\n第二行' }])
  })
  it('deduplicates large sequence numbers without losing integer precision', () => {
    const cursor = '9007199254740993'
    expect(acceptFrame(event(cursor), cursor).change).toBeNull()
    expect(acceptFrame(event('9007199254740994'), cursor).cursor).toBe('9007199254740994')
    expect(() => acceptFrame(event('9007199254740996'), cursor)).toThrow('不连续')
    expect(acceptFrame({ event: 'sync.reset', id: '7', data: '{"cursor":"7"}' }, cursor).change?.reset).toBe(true)
  })
  it('rejects malformed events before advancing the cursor', () => {
    expect(() => acceptFrame({ event: 'business.changed', id: '2', data: '{"id":"2","topics":"tasks","resources":[]}' }, '1')).toThrow()
    expect(() => acceptFrame({ event: 'business.changed', id: '2', data: '{"id":"3","topics":[],"resources":[]}' }, '1')).toThrow()
  })
})

const initialTask: Task = { id: 'task-1', plan_id: 'plan-1', plan_title: '测试计划', plan_mode: 'calendar', plan_version_id: 'version-1', milestone_id: null, title: '原任务', description: '', estimate_minutes: 60, scheduled_date: null, position: 1, status: 'todo', version: 1, source: 'user', completed_at: null, is_overdue: false, plan_status: 'active', version_status: 'active' }

describe('multi-client refresh UI', () => {
  it('refreshes once on page return without changing replay progress and removes return listeners on unmount', async () => {
    const stream = streamMock()
    const load = vi.fn(async () => '当前数据')
    function View() { const result = useLoad(load, [], ['tasks']); return <span>{result.data}</span> }
    const view = render(<SyncProvider token="token-a" userID="user-a" onUnauthorized={async () => undefined}><View /></SyncProvider>)
    await screen.findByText('当前数据')
    stream.send('id: 7\nevent: sync.reset\ndata: {"cursor":"7"}\n\nevent: sync.ready\ndata: {}\n\n')
    await waitFor(() => expect(load).toHaveBeenCalledTimes(2))
    const visibility = vi.spyOn(document, 'visibilityState', 'get')
    visibility.mockReturnValue('hidden')
    fireEvent(document, new Event('visibilitychange'))
    fireEvent.focus(window)
    await new Promise((resolve) => setTimeout(resolve, 350))
    expect(load).toHaveBeenCalledTimes(2)
    visibility.mockReturnValue('visible')
    fireEvent(document, new Event('visibilitychange'))
    fireEvent.focus(window)
    fireEvent(window, new Event('pageshow'))
    await waitFor(() => expect(load).toHaveBeenCalledTimes(3))
    expect(sessionStorage.getItem(`studyflow-sync:${API_URL}:user-a`)).toBe('7')
    expect(stream.streams).toHaveLength(1)
    const documentRemove = vi.spyOn(document, 'removeEventListener')
    const windowRemove = vi.spyOn(window, 'removeEventListener')
    view.unmount()
    expect(documentRemove).toHaveBeenCalledWith('visibilitychange', expect.any(Function))
    expect(windowRemove).toHaveBeenCalledWith('focus', expect.any(Function))
    expect(windowRemove).toHaveBeenCalledWith('pageshow', expect.any(Function))
    expect(stream.streams[0].signal.aborted).toBe(true)
  })
  it('keeps an unsaved timer note when another client ends or replaces the timer', async () => {
    const stream = streamMock()
    const firstSession = { id: 'session-1', task_id: 'task-1', started_at: '2026-10-14T00:00:00Z', ended_at: null, duration_seconds: 0, note: '', status: 'running' as const }
    let running: typeof firstSession | null = firstSession
    vi.mocked(api).mockImplementation(async (_token, path) => {
      if (path === '/tasks/task-1') return initialTask as never
      if (path === '/tasks/task-1/sessions') return [] as never
      if (path === '/dashboard/today') return { running_session: running ? { session: running } : null } as never
      return undefined as never
    })
    render(<SyncProvider token="token-a" userID="user-a" onUnauthorized={async () => undefined}><TaskDrawer token="token-a" task={initialTask} running={null} onClose={() => undefined} onChanged={() => undefined} /></SyncProvider>)
    await screen.findByLabelText('计时备注')
    fireEvent.change(screen.getByLabelText('计时备注'), { target: { value: '尚未保存的学习记录' } })
    stream.send('id: 0\nevent: sync.reset\ndata: {"cursor":"0"}\n\nevent: sync.ready\ndata: {}\n\n')
    running = null; stream.send(frameText(event('1', ['dashboard', 'sessions'])))
    await screen.findByText('计时已在其他客户端变更，未提交的备注仍保留。请核对后重新编辑或复制保存。')
    expect(screen.getByLabelText('计时备注')).toHaveValue('尚未保存的学习记录')
    running = { ...firstSession, id: 'session-2' }; stream.send(frameText(event('2', ['dashboard', 'sessions'])))
    await waitFor(() => expect(screen.getByRole('button', { name: '结束计时' })).toBeDisabled())
    fireEvent.change(screen.getByLabelText('计时备注'), { target: { value: '当前计时记录' } })
    expect(screen.getByRole('button', { name: '结束计时' })).toBeEnabled()
  })
  it('updates a clean drawer, preserves dirty input and old revision, and blocks a deleted task', async () => {
    const stream = streamMock(); let latest = initialTask; let deleted = false
    vi.mocked(api).mockImplementation(async (_token, path) => {
      if (path === '/tasks/task-1') { if (deleted) throw new APIError('资源不存在', 404); return latest as never }
      if (path === '/tasks/task-1/sessions') return [] as never
      if (path === '/dashboard/today') return { running_session: null } as never
      return undefined as never
    })
    render(<SyncProvider token="token-a" userID="user-a" onUnauthorized={async () => undefined}><TaskDrawer token="token-a" task={initialTask} running={null} onClose={() => undefined} onChanged={() => undefined} /></SyncProvider>)
    await waitFor(() => expect(stream.streams).toHaveLength(1))
    stream.send('id: 0\nevent: sync.reset\ndata: {"cursor":"0"}\n\nevent: sync.ready\ndata: {"cursor":"0"}\n\n')
    latest = { ...initialTask, title: '另一个客户端修改', version: 2 }
    stream.send(frameText(event('1')))
    await waitFor(() => expect(screen.getByLabelText('任务名称')).toHaveValue('另一个客户端修改'))
    fireEvent.change(screen.getByLabelText('任务名称'), { target: { value: '未保存输入' } })
    latest = { ...initialTask, title: '再次修改', version: 3 }
    // Return-to-page refresh must preserve the same dirty snapshot as SSE.
    fireEvent.focus(window)
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('数据已在其他客户端更新'))
    expect(screen.getByLabelText('任务名称')).toHaveValue('未保存输入')
    expect(screen.getByRole('button', { name: '保存修改' })).toBeDisabled()
    expect(screen.getByText('2', { selector: 'strong' })).toBeInTheDocument()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    fireEvent.click(screen.getByRole('button', { name: '重新加载最新数据' }))
    await waitFor(() => expect(screen.getByLabelText('任务名称')).toHaveValue('再次修改'))
    fireEvent.change(screen.getByLabelText('任务名称'), { target: { value: '保留被删任务输入' } })
    deleted = true; stream.send(frameText(event('2')))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('任务已被删除'))
    expect(screen.getByLabelText('任务名称')).toHaveValue('保留被删任务输入')
    expect(screen.getByRole('button', { name: '保存修改' })).toBeDisabled()
  })
  it('retries failed refreshes, coalesces duplicate events and cleans connections on account change', async () => {
    const stream = streamMock(); let value = '旧数据'; let rejectNext = false; let reads = 0
    const load = vi.fn(async () => { reads++; if (rejectNext) { rejectNext = false; throw new Error('temporary') }; return value })
    function View({ token }: { token: string }) { const result = useLoad(load, [token], ['tasks']); return <><span>{result.data}</span><SyncStatus /></> }
    const view = render(<SyncProvider token="token-a" userID="user-a" onUnauthorized={async () => undefined}><View token="token-a" /></SyncProvider>)
    await screen.findByText('旧数据'); await waitFor(() => expect(stream.streams).toHaveLength(1))
    stream.send('id: 0\nevent: sync.reset\ndata: {"cursor":"0"}\n\nevent: sync.ready\ndata: {}\n\n')
    await new Promise((resolve) => setTimeout(resolve, 350))
    const before = reads; value = '新数据'; rejectNext = true
    stream.send(frameText(event('1')) + frameText(event('1')))
    await waitFor(() => expect(reads).toBe(before + 1))
    await waitFor(() => expect(screen.getByText('新数据')).toBeInTheDocument(), { timeout: 2500 })
    expect(reads).toBe(before + 2)
    expect(sessionStorage.getItem(`studyflow-sync:${API_URL}:user-a`)).toBe('1')
    view.rerender(<SyncProvider token="token-b" userID="user-b" onUnauthorized={async () => undefined}><View token="token-b" /></SyncProvider>)
    await waitFor(() => expect(stream.streams).toHaveLength(2))
    expect(stream.streams[0].signal.aborted).toBe(true)
    expect(stream.streams[1].headers.get('Authorization')).toBe('Bearer token-b')
    expect(stream.streams[1].headers.has('Last-Event-ID')).toBe(false)
    expect(sessionStorage.getItem(`studyflow-sync:${API_URL}:user-a`)).toBeNull()
    view.unmount(); expect(stream.streams[1].signal.aborted).toBe(true)
  })
})
