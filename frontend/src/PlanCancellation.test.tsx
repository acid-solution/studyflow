import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import { APIError, api, refreshAccess } from './api'
import type { PlanDetail, PlanVersion, Task } from './types'

vi.mock('./api', async () => ({
  ...await vi.importActual<typeof import('./api')>('./api'),
  api: vi.fn(), refreshAccess: vi.fn(),
}))
vi.mock('./sync', () => ({
  SyncProvider: ({ children }: { children: ReactNode }) => children,
  SyncStatus: () => null, clearSyncCursor: vi.fn(), useSync: () => null,
}))

afterEach(() => {
  cleanup()
  vi.resetAllMocks()
  window.history.replaceState(null, '', '/')
})

function setup(status: PlanVersion['status'] = 'draft', active = true, failCancel = false) {
  window.history.replaceState(null, '', '/?plan_id=plan-1&version_id=version-2')
  vi.mocked(refreshAccess).mockResolvedValue({
    access_token: 'test-access', expires_at: '2026-10-14T12:00:00Z',
    account: { user: { id: 'test-user', status: 'active' }, identities: [{ kind: 'email', value: 'test@example.test', verified_at: '' }] },
  })
  const task = (id: string, title: string, state: Task['status']): Task => ({
    id, title, status: state, description: '', milestone_id: null, plan_version_id: '',
    scheduled_date: null, estimate_minutes: 30,
    position: 0, version: 1, source: 'user', completed_at: null, is_overdue: false,
  })
  const version = (id: string, number: number, state: PlanVersion['status'], taskTitle: string): PlanVersion => ({
    id, version_no: number, status: state, mode: 'sequence', source_version_id: null,
    weekly_capacity_minutes: 240, start_date: null, end_date: null, structure_revision: 1,
    milestones: [], unassigned_tasks: [task(id + '-task', taskTitle, state === 'active' ? 'done' : 'todo')],
  })
  const detail: PlanDetail = {
    plan: { id: 'plan-1', goal_id: 'goal-1', active_version_id: active ? 'version-1' : null,
      title: '知识准备计划', objective: '完成准备', description: '', success_criteria: '', target_date: null,
      status: 'active', revision: 1, achieved_at: null, excluded_from_goal_completion: false,
      completion_summary: { has_active_version: active, total_tasks: active ? 1 : 0, done_tasks: active ? 1 : 0, open_tasks: 0 },
      source: 'user', created_at: '2026-10-14T00:00:00Z' },
    versions: [version('version-2', 2, status, '草稿专用任务'), ...(active ? [version('version-1', 1, 'active', '保留的已完成任务')] : [])],
  }
  vi.mocked(api).mockImplementation(async (_token, path, options = {}) => {
    if (path === '/plans/plan-1') return structuredClone(detail) as never
    if (path === '/plan-versions/version-2/cancel' && options.method === 'POST') {
      if (failCancel) throw new APIError('取消失败，请重试', 409)
      detail.versions[0].status = 'canceled'
      return structuredClone(detail.versions[0]) as never
    }
    if (path === '/dashboard/today') return { running_session: null } as never
    return [] as never
  })
  render(<App />)
  return detail
}

describe('canceled plan draft presentation', () => {
  it('returns to the active version after cancellation and only exposes canceled drafts on request', async () => {
    const detail = setup()
    fireEvent.click(await screen.findByRole('button', { name: '取消草稿' }))
    await waitFor(() => expect(screen.queryByRole('button', { name: /^V2/ })).not.toBeInTheDocument())
    expect(screen.getByRole('button', { name: /V1.*当前版本/ })).toBeInTheDocument()
    expect(screen.getByText('保留的已完成任务')).toBeInTheDocument()
    expect(screen.queryByText('草稿专用任务')).not.toBeInTheDocument()
    expect(detail.versions[1].unassigned_tasks[0].status).toBe('done')

    fireEvent.click(screen.getByRole('button', { name: /查看已取消草稿/ }))
    fireEvent.click(screen.getByRole('button', { name: /V2.*已取消/ }))
    expect(screen.getByText(/此草稿已取消，仅保留记录/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '激活 V2' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '编辑版本设置' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '以 V2 为模板' })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '收起已取消草稿' }))
    expect(await screen.findByText('保留的已完成任务')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^V2/ })).not.toBeInTheDocument()
    expect(vi.mocked(api).mock.calls.filter(([, , options]) => options?.method === 'POST')).toHaveLength(1)
  })

  it('ignores a canceled version deep link until the user opens the canceled records', async () => {
    setup('canceled')
    expect(await screen.findByText('保留的已完成任务')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^V2/ })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /查看已取消草稿/ })).toBeInTheDocument()
  })

  it('shows an empty state rather than canceled tasks when no active version exists', async () => {
    setup('canceled', false)
    expect(await screen.findByText(/没有当前版本或可用草稿/)).toBeInTheDocument()
    expect(screen.queryByText('草稿专用任务')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '新建草稿' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /查看已取消草稿/ }))
    expect(await screen.findByText('草稿专用任务')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '激活 V2' })).not.toBeInTheDocument()
  })

  it('keeps the draft selected when cancellation fails', async () => {
    setup('draft', true, true)
    fireEvent.click(await screen.findByRole('button', { name: '取消草稿' }))
    expect(await screen.findByText('取消失败，请重试')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /V2.*草稿/ })).toHaveClass('is-current')
    expect(screen.getByRole('button', { name: '激活 V2' })).toBeInTheDocument()
    expect(screen.getByText('草稿专用任务')).toBeInTheDocument()
  })
})
