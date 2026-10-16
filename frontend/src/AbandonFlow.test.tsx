import { StrictMode, type ReactNode } from 'react'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, refreshAccess } from './api'
import App from './App'
import AbandonFlow from './AbandonFlow'
import type { Preferences } from './types'

vi.mock('./api', async () => ({ ...await vi.importActual<typeof import('./api')>('./api'), api: vi.fn(), refreshAccess: vi.fn() }))
vi.mock('./sync', () => ({ SyncProvider: ({ children }: { children: ReactNode }) => children, SyncStatus: () => null, clearSyncCursor: vi.fn(), useSync: () => null }))
afterEach(() => { cleanup(); vi.resetAllMocks(); window.history.replaceState(null, '', '/') })
const preference: Preferences = { timezone: 'Asia/Shanghai', week_start: 'monday', default_plan_mode: 'calendar', show_completed_today: true, default_abandon_parent_effect: 'ask' }
function setup(defaultEffect: Preferences['default_abandon_parent_effect'] = 'ask', kind: 'goal' | 'plan' = 'goal', hasParent = true) {
  const onClose = vi.fn(), onAbandoned = vi.fn()
  vi.mocked(api).mockImplementation(async (_token, path, options = {}) => (path === '/preferences' && !options.method ? { ...preference, default_abandon_parent_effect: defaultEffect } : {}) as never)
  render(<AbandonFlow token="token" kind={kind} id="item" title="鉴权实践" hasParent={hasParent} onClose={onClose} onAbandoned={onAbandoned} />)
  return { onClose, onAbandoned }
}
const writes = () => vi.mocked(api).mock.calls.filter(([, , options]) => options?.method)

describe('abandon confirmation and account defaults', () => {
  it('uses readable options, requires confirmation, and cancellation writes nothing', async () => {
    const { onClose } = setup()
    expect(await screen.findByRole('dialog', { name: '放弃目标' })).toBeInTheDocument()
    expect(writes()).toHaveLength(0)
    fireEvent.click(screen.getByRole('radio', { name: /从上层完成判断中排除/ }))
    fireEvent.click(screen.getByRole('button', { name: '继续保留' }))
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(writes()).toHaveLength(0)
  })

  it('uses a one-time choice without silently changing the default', async () => {
    const { onAbandoned } = setup('ask', 'plan')
    fireEvent.click(await screen.findByRole('radio', { name: /从上层完成判断中排除/ }))
    fireEvent.click(screen.getByRole('button', { name: '确认放弃计划' }))
    await waitFor(() => expect(onAbandoned).toHaveBeenCalled())
    expect(writes()).toHaveLength(1)
    expect(writes()[0][1]).toBe('/plans/item/abandon')
    expect(JSON.parse(String(writes()[0][2]?.body))).toEqual({ parent_effect: 'exclude' })
  })

  it('persists an explicitly remembered choice after abandonment succeeds', async () => {
    const { onAbandoned } = setup()
    fireEvent.click(await screen.findByRole('radio', { name: /从上层完成判断中排除/ }))
    fireEvent.click(screen.getByRole('checkbox', { name: /以后按此选择直接放弃/ }))
    fireEvent.click(screen.getByRole('button', { name: '确认放弃目标' }))
    await waitFor(() => expect(onAbandoned).toHaveBeenCalledWith(expect.stringContaining('以后将直接使用此选择')))
    expect(writes().map(([, path]) => path)).toEqual(['/goals/item/abandon', '/preferences'])
    expect(JSON.parse(String(writes()[1][2]?.body))).toEqual({ default_abandon_parent_effect: 'exclude' })
  })

  it.each(['exclude', 'block'] as const)('skips all dialogs when the saved default is %s', async choice => {
    const { onAbandoned } = setup(choice, 'plan')
    await waitFor(() => expect(onAbandoned).toHaveBeenCalledTimes(1))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(writes()).toHaveLength(1)
    expect(JSON.parse(String(writes()[0][2]?.body))).toEqual({ parent_effect: choice })
  })

  it('does not automatically abandon or guess a default when preferences cannot be loaded', async () => {
    vi.mocked(api).mockRejectedValue(new Error('偏好读取失败'))
    render(<AbandonFlow token="token" kind="plan" id="p1" title="计划" hasParent onClose={vi.fn()} onAbandoned={vi.fn()} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('偏好读取失败')
    expect(writes()).toHaveLength(0)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('keeps the choice after a failed abandonment and does not save a default', async () => {
    setup()
    await screen.findByRole('dialog')
    vi.mocked(api).mockRejectedValue(new Error('请先恢复上层目标'))
    fireEvent.click(screen.getByRole('radio', { name: /从上层完成判断中排除/ }))
    fireEvent.click(screen.getByRole('checkbox', { name: /以后按此选择直接放弃/ }))
    fireEvent.click(screen.getByRole('button', { name: '确认放弃目标' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('请先恢复上层目标')
    expect(screen.getByRole('radio', { name: /从上层完成判断中排除/ })).toBeChecked()
    expect(writes().map(([, path]) => path)).toEqual(['/goals/item/abandon'])
  })

  it('reports a failed preference save without repeating a successful abandonment', async () => {
    const { onAbandoned } = setup()
    await screen.findByRole('dialog')
    vi.mocked(api).mockImplementation(async (_token, path) => { if (path === '/preferences') throw new Error('写入失败'); return {} as never })
    fireEvent.click(screen.getByRole('checkbox', { name: /以后按此选择直接放弃/ }))
    fireEvent.click(screen.getByRole('button', { name: '确认放弃目标' }))
    await waitFor(() => expect(onAbandoned).toHaveBeenCalledWith(expect.stringContaining('默认选择未能保存')))
    expect(writes().filter(([, path]) => path === '/goals/item/abandon')).toHaveLength(1)
  })

  it('confirms a root goal without displaying irrelevant parent choices', async () => {
    const { onAbandoned } = setup('ask', 'goal', false)
    await screen.findByRole('dialog')
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '确认放弃目标' }))
    await waitFor(() => expect(onAbandoned).toHaveBeenCalled())
    expect(JSON.parse(String(writes()[0][2]?.body))).toEqual({})
  })

  it('does not duplicate an automatic abandonment during StrictMode effect replay', async () => {
    const onAbandoned = vi.fn()
    vi.mocked(api).mockImplementation(async (_token, path, options = {}) => (path === '/preferences' && !options.method ? { ...preference, default_abandon_parent_effect: 'block' } : {}) as never)
    render(<StrictMode><AbandonFlow token="token" kind="goal" id="g1" title="目标" hasParent onClose={vi.fn()} onAbandoned={onAbandoned} /></StrictMode>)
    await waitFor(() => expect(onAbandoned).toHaveBeenCalledTimes(1))
    expect(writes()).toHaveLength(1)
  })

  it('can change a remembered default back to ask from settings', async () => {
    let stored: Preferences = { ...preference, default_abandon_parent_effect: 'exclude' }
    vi.mocked(refreshAccess).mockResolvedValue({ access_token: 'access', expires_at: '', account: { user: { id: 'user', status: 'active' }, identities: [] } })
    vi.mocked(api).mockImplementation(async (_token, path, options = {}) => {
      if (path === '/dashboard/today') return { date: '', overdue: [], today: [], completed_today: [], sequence_plans: [], future: [], running_session: null } as never
      if (path === '/preferences') {
        if (options.method === 'PATCH') stored = { ...stored, ...JSON.parse(String(options.body)) }
        return stored as never
      }
      return [] as never
    })
    render(<App />)
    fireEvent.click(await screen.findByRole('button', { name: '设置' }))
    const select = await screen.findByRole('combobox', { name: '放弃操作' })
    expect(select).toHaveValue('exclude')
    fireEvent.change(select, { target: { value: 'ask' } })
    await waitFor(() => expect(select).toHaveValue('ask'))
    expect(stored.default_abandon_parent_effect).toBe('ask')
    expect(stored.week_start).toBe('monday')
  })
})
