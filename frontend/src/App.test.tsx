import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import { APIError, api, refreshAccess } from './api'

vi.mock('./api', async () => {
  const actual = await vi.importActual<typeof import('./api')>('./api')
  return {
    ...actual,
    api: vi.fn(),
    refreshAccess: vi.fn().mockRejectedValue(new Error('no session')),
    requestVerification: vi.fn().mockResolvedValue({ challenge_id: 'challenge', expires_at: '', resend_after: '' }),
  }
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('StudyFlow authentication entry', () => {
  it('shows the login form when refresh cookie is unavailable', async () => {
    render(<App />)
    expect(await screen.findByPlaceholderText('name@example.com')).toBeInTheDocument()
    expect(screen.getByText('把长期目标变成今天可以完成的行动')).toBeInTheDocument()
  })

  it('requests a registration code before asking for the password', async () => {
    render(<App />)
    await screen.findByPlaceholderText('name@example.com')
    fireEvent.click(screen.getByRole('button', { name: '注册' }))
    fireEvent.change(screen.getByPlaceholderText('name@example.com'), { target: { value: 'person@example.com' } })
    fireEvent.click(screen.getByRole('button', { name: '发送验证码' }))
    await waitFor(() => expect(screen.getByPlaceholderText('000000')).toBeInTheDocument())
    expect(screen.getByPlaceholderText('至少 8 位')).toBeInTheDocument()
  })

  it('creates a plan with its own required objective', async () => {
    vi.mocked(refreshAccess).mockResolvedValueOnce({
      access_token: 'access-token', expires_at: '2026-10-03T16:00:00Z',
      account: { user: { id: 'user-id', status: 'active' }, identities: [{ kind: 'email', value: 'person@example.com', verified_at: '2026-10-03T00:00:00Z' }] },
    })
    const goal = {
      id: 'goal-1', parent_goal_id: null, title: '掌握后端开发', description: '', success_criteria: '', target_date: null,
      status: 'active' as const, version: 1, achieved_at: null, excluded_from_parent_completion: false, ready_to_complete: false,
      completion_summary: { included_children: 0, achieved_children: 0, incomplete_children: 0, excluded_children: 0 }, plans: [], children: [],
    }
    const detail = {
      plan: {
        id: 'plan-1', goal_id: goal.id, active_version_id: null, title: '鉴权学习计划', description: '',
        objective: '能够独立实现鉴权系统', success_criteria: '', target_date: null, status: 'active' as const,
        revision: 1, achieved_at: null, excluded_from_goal_completion: false,
        completion_summary: { has_active_version: false, total_tasks: 0, done_tasks: 0, open_tasks: 0 },
        source: 'user' as const, created_at: '2026-10-03T00:00:00Z',
      },
      versions: [{ id: 'version-1', version_no: 1, status: 'draft' as const, mode: 'calendar' as const, source_version_id: null, weekly_capacity_minutes: 600, start_date: null, end_date: null, structure_revision: 1, milestones: [], unassigned_tasks: [] }],
    }
    vi.mocked(api).mockImplementation(async (_token, path, options = {}) => {
      if (path === '/dashboard/today') return { date: '2026-10-03', overdue: [], today: [], completed_today: [], sequence_plans: [], future: [], running_session: null } as never
      if (path === '/goals/tree') return [goal] as never
      if (path === '/plans' && !options.method) return [] as never
      if (path === `/goals/${goal.id}/plans` && options.method === 'POST') return detail as never
      if (path === `/plans/${detail.plan.id}`) return detail as never
      return [] as never
    })

    render(<App />)
    fireEvent.click(await screen.findByRole('button', { name: '学习计划' }))
    fireEvent.click(await screen.findByRole('button', { name: '新建计划' }))
    fireEvent.change(screen.getByPlaceholderText('计划名称'), { target: { value: '鉴权学习计划' } })
    fireEvent.change(screen.getByPlaceholderText('完成计划后要达成什么目标'), { target: { value: '能够独立实现鉴权系统' } })
    fireEvent.click(screen.getByRole('button', { name: '创建' }))

    await waitFor(() => {
      const call = vi.mocked(api).mock.calls.find(([, path, options]) => path === `/goals/${goal.id}/plans` && options?.method === 'POST')
      expect(call).toBeDefined()
      expect(JSON.parse(String(call?.[2]?.body))).toMatchObject({ title: '鉴权学习计划', objective: '能够独立实现鉴权系统', mode: 'calendar' })
    })
  })

  it('switches a superseded version back to the current version', async () => {
    vi.mocked(refreshAccess).mockResolvedValueOnce({
      access_token: 'access-token', expires_at: '2026-10-03T16:00:00Z',
      account: { user: { id: 'user-id', status: 'active' }, identities: [{ kind: 'email', value: 'person@example.com', verified_at: '2026-10-03T00:00:00Z' }] },
    })
    const goal = {
      id: 'goal-1', parent_goal_id: null, title: '掌握后端开发', description: '', success_criteria: '', target_date: null,
      status: 'active' as const, version: 1, achieved_at: null, excluded_from_parent_completion: false, ready_to_complete: false,
      completion_summary: { included_children: 1, achieved_children: 0, incomplete_children: 1, excluded_children: 0 }, plans: [], children: [],
    }
    const plan = {
      id: 'plan-1', goal_id: goal.id, active_version_id: 'version-2', title: '鉴权学习计划', description: '',
      objective: '能够独立实现鉴权系统', success_criteria: '', target_date: null, status: 'active' as const,
      revision: 1, achieved_at: null, excluded_from_goal_completion: false,
      completion_summary: { has_active_version: true, total_tasks: 0, done_tasks: 0, open_tasks: 0 },
      source: 'user' as const, created_at: '2026-10-03T00:00:00Z',
    }
    const versions = [
      { id: 'version-2', version_no: 2, status: 'active' as const, mode: 'sequence' as const, source_version_id: 'version-1', weekly_capacity_minutes: 480, start_date: null, end_date: null, structure_revision: 1, milestones: [], unassigned_tasks: [] },
      { id: 'version-1', version_no: 1, status: 'superseded' as const, mode: 'calendar' as const, source_version_id: null, weekly_capacity_minutes: 600, start_date: null, end_date: null, structure_revision: 2, milestones: [], unassigned_tasks: [] },
    ]
    vi.mocked(api).mockImplementation(async (_token, path, options = {}) => {
      if (path === '/dashboard/today') return { date: '2026-10-03', overdue: [], today: [], completed_today: [], sequence_plans: [], future: [], running_session: null } as never
      if (path === '/goals/tree') return [goal] as never
      if (path === '/plans' && !options.method) return [{ ...plan, active_version_no: 2, draft_version_id: null, current_mode: 'sequence', done_tasks: 0, total_tasks: 0 }] as never
      if (path === `/plans/${plan.id}`) return { plan, versions } as never
      if (path === '/plan-versions/version-1/activate' && options.method === 'POST') return { plan: { ...plan, active_version_id: 'version-1' }, versions } as never
      return [] as never
    })

    render(<App />)
    fireEvent.click(await screen.findByRole('button', { name: '学习计划' }))
    fireEvent.click(await screen.findByRole('button', { name: /查看计划/ }))
    fireEvent.click(await screen.findByRole('button', { name: /V1/ }))
    fireEvent.click(screen.getByRole('button', { name: '切换到 V1' }))

    await waitFor(() => expect(vi.mocked(api).mock.calls.some(([, path, options]) => path === '/plan-versions/version-1/activate' && options?.method === 'POST')).toBe(true))
  })

  it('requires a second explicit confirmation before completing an unfinished goal', async () => {
    vi.mocked(refreshAccess).mockResolvedValueOnce({
      access_token: 'access-token', expires_at: '2026-10-03T16:00:00Z',
      account: { user: { id: 'user-id', status: 'active' }, identities: [{ kind: 'email', value: 'person@example.com', verified_at: '2026-10-03T00:00:00Z' }] },
    })
    const goal = {
      id: 'goal-1', parent_goal_id: null, title: '拿到后端实习', description: '', success_criteria: '', target_date: null,
      status: 'active' as const, version: 1, achieved_at: null, excluded_from_parent_completion: false, ready_to_complete: false,
      completion_summary: { included_children: 2, achieved_children: 1, incomplete_children: 1, excluded_children: 0 }, plans: [], children: [],
    }
    let completionAttempts = 0
    vi.mocked(api).mockImplementation(async (_token, path, options = {}) => {
      if (path === '/dashboard/today') return { date: '2026-10-03', overdue: [], today: [], completed_today: [], sequence_plans: [], future: [], running_session: null } as never
      if (path === '/goals/tree') return [goal] as never
      if (path === `/goals/${goal.id}/complete` && options.method === 'POST') {
        completionAttempts += 1
        if (completionAttempts === 1) throw new APIError('目标仍有未达成子项', 409, 'completion_confirmation_required', goal.completion_summary)
        return { ...goal, status: 'achieved' } as never
      }
      return [] as never
    })
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)

    render(<App />)
    fireEvent.click(await screen.findByRole('button', { name: '目标' }))
    fireEvent.click(await screen.findByRole('button', { name: '确认完成' }))

    await waitFor(() => expect(completionAttempts).toBe(2))
    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('仍有 1 个直属子项未达成'))
    const acknowledged = vi.mocked(api).mock.calls.find(([, path, options]) => path === `/goals/${goal.id}/complete` && options?.body)
    expect(JSON.parse(String(acknowledged?.[2]?.body))).toEqual({ acknowledge_incomplete: true })
    confirm.mockRestore()
  })
})
