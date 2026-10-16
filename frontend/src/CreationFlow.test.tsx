import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './api'
import { GoalEditor, PlanCreator, VersionEditor } from './CreationDialogs'
import PlanOutline from './PlanOutline'
import type { GoalNode, PlanDetail, PlanVersion } from './types'

vi.mock('./api', async () => ({ ...await vi.importActual<typeof import('./api')>('./api'), api: vi.fn() }))
afterEach(() => { cleanup(); vi.resetAllMocks(); vi.restoreAllMocks() })

const goal = (id: string, children: GoalNode[] = []): GoalNode => ({ id, title: id, description: '', success_criteria: '', parent_goal_id: null, status: 'active', version: 1, target_date: null, achieved_at: null, excluded_from_parent_completion: false, ready_to_complete: false, completion_summary: { included_children: 0, achieved_children: 0, incomplete_children: 0, excluded_children: 0 }, children, plans: [] })
const version: PlanVersion = { id: 'v1', version_no: 1, mode: 'calendar', status: 'draft', source_version_id: null, weekly_capacity_minutes: 600, start_date: null, end_date: null, structure_revision: 1, milestones: [{ id: 'm1', title: '注册与登录', outcome: '', position: 1, tasks: [] }], unassigned_tasks: [] }
const plan: PlanDetail['plan'] = { id: 'p1', goal_id: 'g1', title: '鉴权学习计划', objective: '独立实现鉴权', description: '', success_criteria: '', target_date: null, status: 'active', revision: 1, active_version_id: null, achieved_at: null, excluded_from_goal_completion: false, source: 'user', created_at: '', completion_summary: { has_active_version: false, total_tasks: 0, done_tasks: 0, open_tasks: 0 } }

function fillPlan() {
  fireEvent.change(screen.getByLabelText(/计划名称/), { target: { value: '鉴权学习计划' } })
  fireEvent.change(screen.getByPlaceholderText('完成计划后要达成什么目标'), { target: { value: '独立实现并解释鉴权链路' } })
}

describe('guided manual planning', () => {
  it('creates a first goal inside the plan flow without losing the entered plan', async () => {
    const onCreated = vi.fn(), onGoalsChanged = vi.fn()
    vi.mocked(api).mockImplementation(async (_token, path) => (path === '/goals' ? goal('new-goal') : { plan: { id: 'new-plan' } }) as never)
    render(<PlanCreator token="token" goals={[]} onClose={vi.fn()} onCreated={onCreated} onGoalsChanged={onGoalsChanged} />)
    fillPlan()
    fireEvent.click(screen.getByRole('button', { name: '新建目标' }))
    const goalDialog = screen.getByRole('dialog', { name: '新建目标' })
    fireEvent.change(within(goalDialog).getByLabelText(/目标名称/), { target: { value: '独立交付后端服务' } })
    fireEvent.click(within(goalDialog).getByRole('button', { name: '创建目标' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '新建目标' })).not.toBeInTheDocument())
    expect(screen.getByLabelText(/计划名称/)).toHaveValue('鉴权学习计划')
    expect(screen.getByRole('combobox', { name: /所属目标/ })).toHaveValue('new-goal')
    expect(onGoalsChanged).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    expect(api).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: '创建并安排任务' }))
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('new-plan'))
    expect(vi.mocked(api).mock.calls[1][1]).toBe('/goals/new-goal/plans')
  })

  it('retains all inputs on back navigation and converts hours to minutes only on submission', async () => {
    const goals = [goal('求职准备', [goal('后端能力')])]
    const created = vi.fn()
    vi.mocked(api).mockResolvedValue({ plan: { id: 'p1' } } as never)
    render(<PlanCreator token="token" goals={goals} onClose={vi.fn()} onCreated={created} onGoalsChanged={vi.fn()} />)
    fillPlan()
    fireEvent.change(screen.getByRole('combobox', { name: /所属目标/ }), { target: { value: '后端能力' } })
    expect(screen.getByRole('option', { name: '求职准备 / 后端能力' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    fireEvent.click(screen.getByRole('radio', { name: /按课时推进/ }))
    fireEvent.change(screen.getByLabelText(/每周预计投入/), { target: { value: '3.5' } })
    fireEvent.click(screen.getByRole('button', { name: '上一步' }))
    expect(screen.getByLabelText(/计划名称/)).toHaveValue('鉴权学习计划')
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    expect(screen.getByRole('radio', { name: /按课时推进/ })).toBeChecked()
    fireEvent.click(screen.getByRole('button', { name: '创建并安排任务' }))
    await waitFor(() => expect(created).toHaveBeenCalled())
    expect(JSON.parse(String(vi.mocked(api).mock.calls[0][2]?.body))).toMatchObject({ mode: 'sequence', weekly_capacity_minutes: 210, objective: '独立实现并解释鉴权链路' })
  })

  it('does not submit invalid date ranges and keeps inputs after a server error', async () => {
    vi.mocked(api).mockRejectedValue(new Error('服务器暂时不可用'))
    render(<PlanCreator token="token" goals={[goal('g1')]} initialGoalID="g1" onClose={vi.fn()} onCreated={vi.fn()} onGoalsChanged={vi.fn()} />)
    fillPlan()
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    fireEvent.change(screen.getByLabelText(/开始日期/), { target: { value: '2026-10-20' } })
    fireEvent.change(screen.getByLabelText(/结束日期/), { target: { value: '2026-10-10' } })
    fireEvent.submit(screen.getByRole('button', { name: '创建并安排任务' }).closest('form')!)
    expect(api).not.toHaveBeenCalled()
    expect(screen.getByRole('alert')).toHaveTextContent('请检查')
    fireEvent.change(screen.getByLabelText(/结束日期/), { target: { value: '2026-11-10' } })
    fireEvent.click(screen.getByRole('button', { name: '创建并安排任务' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('服务器暂时不可用')
    expect(screen.getByLabelText(/开始日期/)).toHaveValue('2026-10-20')
    fireEvent.click(screen.getByRole('button', { name: '上一步' }))
    expect(screen.getByLabelText(/计划名称/)).toHaveValue('鉴权学习计划')
  })

  it('prevents duplicate creation while the first request is pending', async () => {
    let finish!: (value: never) => void
    vi.mocked(api).mockImplementation(() => new Promise(resolve => { finish = resolve }))
    render(<PlanCreator token="token" goals={[goal('g1')]} initialGoalID="g1" onClose={vi.fn()} onCreated={vi.fn()} onGoalsChanged={vi.fn()} />)
    fillPlan(); fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    const form = screen.getByRole('button', { name: '创建并安排任务' }).closest('form')!
    fireEvent.submit(form); fireEvent.submit(form)
    expect(api).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('button', { name: '创建中…' })).toBeDisabled()
    finish({ plan: { id: 'p1' } } as never)
    await waitFor(() => expect(screen.queryByText('创建中…')).not.toBeInTheDocument())
  })

  it('excludes the edited goal and its descendants from parent choices and preserves edits when stale', () => {
    const original = goal('root', [goal('child', [goal('grandchild')])])
    const { rerender } = render(<GoalEditor token="token" goals={[original, goal('other')]} initial={original} onClose={vi.fn()} onSaved={vi.fn()} />)
    expect(screen.queryByRole('option', { name: /grandchild/ })).not.toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'other' })).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText(/目标名称/), { target: { value: '本地未保存修改' } })
    rerender(<GoalEditor token="token" goals={[{ ...original, version: 2 }, goal('other')]} initial={original} onClose={vi.fn()} onSaved={vi.fn()} />)
    expect(screen.getByLabelText(/目标名称/)).toHaveValue('本地未保存修改')
    expect(screen.getByRole('button', { name: '保存修改' })).toBeDisabled()
    expect(screen.getByRole('alert')).toHaveTextContent('输入已保留')
  })

  it('saves goal details and explicitly clears a removed target date with the original lock version', async () => {
    const original = { ...goal('g1'), target_date: '2026-12-01' }
    vi.mocked(api).mockResolvedValue(original as never)
    const saved = vi.fn()
    render(<GoalEditor token="token" goals={[original]} initial={original} onClose={vi.fn()} onSaved={saved} />)
    fireEvent.change(screen.getByLabelText(/目标日期/), { target: { value: '' } })
    fireEvent.change(screen.getByLabelText(/完成标准/), { target: { value: '独立上线并验收' } })
    fireEvent.click(screen.getByRole('button', { name: '保存修改' }))
    await waitFor(() => expect(saved).toHaveBeenCalled())
    expect(JSON.parse(String(vi.mocked(api).mock.calls[0][2]?.body))).toMatchObject({ version: 1, clear_target_date: true, success_criteria: '独立上线并验收', clear_parent: true })
  })

  it('adds a task to the stage selected from its own card and retains the form on failure', async () => {
    vi.mocked(api).mockRejectedValueOnce(new Error('保存失败')).mockResolvedValue({} as never)
    const reload = vi.fn().mockResolvedValue(undefined)
    render(<PlanOutline token="token" plan={plan} version={version} reload={reload} onOpenTask={vi.fn()} />)
    fireEvent.click(within(screen.getByText('注册与登录').closest('section')!).getByRole('button', { name: '添加任务' }))
    expect(screen.getByLabelText('所属阶段')).toHaveValue('m1')
    fireEvent.change(screen.getByLabelText(/任务名称/), { target: { value: '编写登录接口测试' } })
    fireEvent.change(screen.getByLabelText(/安排日期/), { target: { value: '2026-10-20' } })
    fireEvent.change(screen.getByLabelText('预计用时（分钟）'), { target: { value: '45' } })
    fireEvent.click(screen.getByRole('button', { name: '保存任务' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('保存失败')
    expect(screen.getByLabelText(/任务名称/)).toHaveValue('编写登录接口测试')
    fireEvent.click(screen.getByRole('button', { name: '保存任务' }))
    await waitFor(() => expect(reload).toHaveBeenCalledTimes(1))
    expect(JSON.parse(String(vi.mocked(api).mock.calls[1][2]?.body))).toMatchObject({ milestone_id: 'm1', scheduled_date: '2026-10-20', estimate_minutes: 45 })
  })

  it('does not ask for a date in sequence plans and supports consecutive task entry', async () => {
    vi.mocked(api).mockResolvedValue({} as never)
    render(<PlanOutline token="token" plan={plan} version={{ ...version, mode: 'sequence' }} reload={vi.fn().mockResolvedValue(undefined)} onOpenTask={vi.fn()} />)
    fireEvent.click(screen.getAllByRole('button', { name: '添加任务' })[0])
    expect(screen.queryByLabelText(/安排日期/)).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText(/任务名称/), { target: { value: '第一课' } })
    fireEvent.click(screen.getByLabelText('保存后继续添加'))
    fireEvent.click(screen.getByRole('button', { name: '保存任务' }))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('第一课'))
    expect(screen.getByLabelText(/任务名称/)).toHaveValue('')
    expect(JSON.parse(String(vi.mocked(api).mock.calls[0][2]?.body))).not.toHaveProperty('scheduled_date')
  })

  it('preserves an open composer but blocks saving if the plan becomes read-only', () => {
    const { rerender } = render(<PlanOutline token="token" plan={plan} version={version} reload={vi.fn()} onOpenTask={vi.fn()} />)
    fireEvent.click(screen.getAllByRole('button', { name: '添加任务' })[0])
    fireEvent.change(screen.getByLabelText(/任务名称/), { target: { value: '未保存任务' } })
    rerender(<PlanOutline token="token" plan={{ ...plan, status: 'achieved' }} version={version} reload={vi.fn()} onOpenTask={vi.fn()} />)
    expect(screen.getByLabelText(/任务名称/)).toHaveValue('未保存任务')
    expect(screen.getByRole('button', { name: '保存任务' })).toBeDisabled()
    expect(screen.getByRole('alert')).toHaveTextContent('输入已保留')
  })

  it('replaces internal mode prompts with a blank-version form', async () => {
    vi.mocked(api).mockResolvedValue({ id: 'v2' } as never)
    const saved = vi.fn()
    render(<VersionEditor token="token" planID="p1" creating version={version} onClose={vi.fn()} onSaved={saved} />)
    fireEvent.click(screen.getByRole('radio', { name: /按课时推进/ }))
    fireEvent.click(screen.getByRole('button', { name: '创建空白草稿' }))
    await waitFor(() => expect(saved).toHaveBeenCalledWith('v2'))
    expect(JSON.parse(String(vi.mocked(api).mock.calls[0][2]?.body))).toMatchObject({ creation_mode: 'blank', mode: 'sequence', weekly_capacity_minutes: 600 })
  })
})
