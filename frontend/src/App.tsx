import { FormEvent, ReactNode, useCallback, useEffect, useState } from 'react'
import {
  AlertCircle, BarChart3, BookOpen, CalendarDays, Check, ChevronDown, ChevronRight,
  Circle, Clock3, Flag, Home, ListChecks, LogOut, Plus, RefreshCcw, Route, Settings,
  ShieldCheck, Target, Timer, X,
} from 'lucide-react'
import { Account, APIError, api, login, logout, refreshAccess, register, requestVerification } from './api'
import type { Dashboard, GoalNode, ImportDraft, Milestone, PlanDetail, PlanImportResult, PlanImportView, PlanMode, PlanSummary, PlanVersion, Preferences, Session, Task, TaskSource, WeeklyReview } from './types'

type Page = 'today' | 'goals' | 'plans' | 'plan-detail' | 'tasks' | 'reviews' | 'imports' | 'settings'

const nav: Array<{ id: Page; label: string; icon: typeof Home }> = [
  { id: 'today', label: '今日', icon: Home },
  { id: 'goals', label: '目标', icon: Target },
  { id: 'plans', label: '学习计划', icon: Route },
  { id: 'tasks', label: '全部任务', icon: ListChecks },
  { id: 'imports', label: '计划导入', icon: RefreshCcw },
  { id: 'reviews', label: '学习复盘', icon: BarChart3 },
  { id: 'settings', label: '设置', icon: Settings },
]

const hours = (seconds: number) => `${(seconds / 3600).toFixed(seconds % 3600 === 0 ? 0 : 1)}h`
const minutes = (value: number) => `${Math.floor(value / 60)}h ${value % 60}m`

function useLoad<T>(loader: () => Promise<T>, deps: unknown[]) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const reload = useCallback(async () => {
    setLoading(true); setError('')
    try { setData(await loader()) } catch (reason) { setError(messageOf(reason)) } finally { setLoading(false) }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
  useEffect(() => { void reload() }, [reload])
  return { data, error, loading, reload }
}

function messageOf(reason: unknown) {
  if (reason instanceof APIError) return reason.message
  if (reason instanceof Error) return reason.message
  return '操作失败，请稍后重试'
}

function AuthPage({ onAuthenticated }: { onAuthenticated: (issue: { access_token: string; account: Account }) => void }) {
  const [mode, setMode] = useState<'login' | 'register'>('login')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [challengeID, setChallengeID] = useState('')
  const [code, setCode] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault(); setBusy(true); setNotice('')
    try {
      if (mode === 'login') {
        onAuthenticated(await login(email, password))
      } else if (!challengeID) {
        const challenge = await requestVerification(email)
        setChallengeID(challenge.challenge_id)
        setNotice('验证码已经发送到邮箱，请填写验证码和密码。')
      } else {
        onAuthenticated(await register(challengeID, code, password))
      }
    } catch (reason) { setNotice(messageOf(reason)) } finally { setBusy(false) }
  }

  return <main className="auth-shell">
    <section className="auth-card">
      <div className="auth-brand"><span className="brand-mark">S</span><div><strong>StudyFlow</strong><span>把长期目标变成今天可以完成的行动</span></div></div>
      <div className="auth-tabs"><button className={mode === 'login' ? 'is-current' : ''} onClick={() => { setMode('login'); setChallengeID(''); setNotice('') }}>登录</button><button className={mode === 'register' ? 'is-current' : ''} onClick={() => { setMode('register'); setChallengeID(''); setNotice('') }}>注册</button></div>
      <form className="auth-form" onSubmit={submit}>
        <label><span>邮箱</span><input type="email" required value={email} onChange={(event) => setEmail(event.target.value)} placeholder="name@example.com" disabled={Boolean(challengeID)} /></label>
        {mode === 'register' && challengeID && <label><span>六位验证码</span><input required pattern="[0-9]{6}" value={code} onChange={(event) => setCode(event.target.value)} placeholder="000000" /></label>}
        {(mode === 'login' || challengeID) && <label><span>密码</span><input type="password" required minLength={8} value={password} onChange={(event) => setPassword(event.target.value)} placeholder="至少 8 位" /></label>}
        {notice && <p className="auth-notice">{notice}</p>}
        <button className="button button-primary" disabled={busy}>{busy ? '处理中…' : mode === 'login' ? '登录' : challengeID ? '完成注册' : '发送验证码'}</button>
      </form>
    </section>
  </main>
}

function LoadingBlock({ error }: { error?: string }) { return <div className="empty-plans"><strong>{error ? '加载失败' : '正在加载'}</strong><span>{error || '正在读取最新数据…'}</span></div> }

function TaskRow({ task, onOpen, onComplete }: { task: Task; onOpen: (task: Task) => void; onComplete: (task: Task) => void }) {
  const done = task.status === 'done'
  return <div className={done ? 'task-row is-complete' : 'task-row'}>
    <button className="task-check" onClick={() => onComplete(task)} aria-label={done ? '重新打开' : '完成任务'}>{done ? <Check size={15} /> : <Circle size={17} />}</button>
    <div className="task-copy"><strong>{task.title}</strong><div className="task-meta"><span className="plan-badge tone-blue">{task.plan_title || '学习计划'}</span><SourceBadge source={task.source} /><span>{task.scheduled_date ? task.scheduled_date : task.plan_mode === 'sequence' ? '按课时推进' : '未安排日期'}</span></div></div>
    <button className="row-action" onClick={() => onOpen(task)} aria-label="查看任务"><ChevronRight size={17} /></button>
  </div>
}

function TodayPage({ token, onOpen }: { token: string; onOpen: (task: Task, reload: () => void) => void }) {
  const { data, error, reload } = useLoad(() => api<Dashboard>(token, '/dashboard/today'), [token])
  const setDone = async (task: Task) => { await api(token, `/tasks/${task.id}/${task.status === 'done' ? 'reopen' : 'complete'}`, { method: 'POST' }); await reload() }
  if (!data) return <PageFrame eyebrow="今日工作台" title="先处理最需要推进的内容" description="日期任务安排到天，课时计划按顺序继续。"><LoadingBlock error={error} /></PageFrame>
  return <PageFrame eyebrow={data.date} title="先处理最需要推进的内容" description="多个计划可以同步进行，计时记录会进入本周复盘。">
    <section className="summary-strip"><div><strong>{data.overdue.length}</strong><span>已逾期</span></div><div><strong>{data.today.filter((task) => task.status !== 'done').length}</strong><span>今天待完成</span></div><div><strong>{data.sequence_plans.length}</strong><span>课时计划进行中</span></div><div className="summary-copy"><CalendarDays size={17} /><span>未来任务按日期依次排列</span></div></section>
    {data.running_session && <section className="running-banner"><Timer size={18} /><div><strong>正在计时：{data.running_session.task.title}</strong><span>开始于 {new Date(data.running_session.session.started_at).toLocaleTimeString('zh-CN')}</span></div><button className="button" onClick={() => onOpen(data.running_session!.task, reload)}>查看</button></section>}
    <Agenda title="已逾期" description="先完成、改期或取消，避免任务持续堆积。" danger count={data.overdue.length}>{data.overdue.map((task) => <TaskRow key={task.id} task={task} onOpen={(value) => onOpen(value, reload)} onComplete={setDone} />)}</Agenda>
    <Agenda title="今天" description="这里只安排日期，不规定几点开始。" count={data.today.length}>{data.today.map((task) => <TaskRow key={task.id} task={task} onOpen={(value) => onOpen(value, reload)} onComplete={setDone} />)}</Agenda>
    <Agenda title="按课时推进" description="每个计划只显示下一项未完成任务。" count={data.sequence_plans.length}><div className="plan-card-grid compact-grid">{data.sequence_plans.map((entry) => <article className="lesson-card" key={entry.plan_id}><div><span className="plan-badge tone-violet">{entry.done_tasks} / {entry.total_tasks} 课</span><h3>{entry.plan_title}</h3></div>{entry.next_task ? <><div className="next-lesson"><span>下一课</span><strong>{entry.next_task.title}</strong></div><button className="button plan-open-button" onClick={() => onOpen(entry.next_task!, reload)}>开始本课 <ChevronRight size={15} /></button></> : <p className="muted-copy">全部课时已经完成</p>}</article>)}</div></Agenda>
    <Agenda title="未来" description="显示未来 30 天内已经确定日期的任务。" count={data.future.length}>{data.future.map((task) => <TaskRow key={task.id} task={task} onOpen={(value) => onOpen(value, reload)} onComplete={setDone} />)}</Agenda>
  </PageFrame>
}

function Agenda({ title, description, count, danger, children }: { title: string; description: string; count: number; danger?: boolean; children: ReactNode }) {
  return <section className={danger ? 'agenda-section overdue-section' : 'agenda-section'}><div className="section-heading"><div className="section-title">{danger ? <AlertCircle size={18} /> : <CalendarDays size={18} />}<h2>{title}</h2><span>{count}</span></div><p>{description}</p></div><div className="task-list">{count ? children : <div className="empty-row">暂无内容</div>}</div></section>
}

function PageFrame({ eyebrow, title, description, action, children }: { eyebrow: string; title: string; description: string; action?: ReactNode; children: ReactNode }) {
  return <div className="page-content"><div className="page-heading"><div><span className="eyebrow">{eyebrow}</span><h1>{title}</h1><p>{description}</p></div>{action}</div>{children}</div>
}

function flattenGoals(nodes: GoalNode[], depth = 0): Array<{ goal: GoalNode; depth: number }> { return nodes.flatMap((goal) => [{ goal, depth }, ...flattenGoals(goal.children, depth + 1)]) }

function GoalsPage({ token, onOpenPlan }: { token: string; onOpenPlan: (id: string) => void }) {
  const { data, error, reload } = useLoad(() => api<GoalNode[]>(token, '/goals/tree'), [token])
  const [editing, setEditing] = useState<GoalNode | null>(null)
  const [parentID, setParentID] = useState('')
  const [title, setTitle] = useState('')
  const [criteria, setCriteria] = useState('')
  const [showForm, setShowForm] = useState(false)
  const all = flattenGoals(data ?? [])
  const reset = () => { setEditing(null); setParentID(''); setTitle(''); setCriteria(''); setShowForm(false) }
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (editing) await api(token, `/goals/${editing.id}`, { method: 'PATCH', body: JSON.stringify({ version: editing.version, title, success_criteria: criteria, parent_goal_id: parentID || undefined, clear_parent: !parentID }) })
    else await api(token, '/goals', { method: 'POST', body: JSON.stringify({ title, success_criteria: criteria, parent_goal_id: parentID || undefined }) })
    reset(); await reload()
  }
  const beginEdit = (goal: GoalNode) => { setEditing(goal); setTitle(goal.title); setCriteria(goal.success_criteria); setParentID(goal.parent_goal_id ?? ''); setShowForm(true) }
  const addChild = (goal: GoalNode) => { reset(); setParentID(goal.id); setShowForm(true) }
  const complete = async (goal: GoalNode) => { await api(token, `/goals/${goal.id}/complete`, { method: 'POST' }); await reload() }
  const abandon = async (goal: GoalNode) => { await api(token, `/goals/${goal.id}/abandon`, { method: 'POST' }); await reload() }
  return <PageFrame eyebrow="目标树" title="把长期目标逐层拆开" description="目标可以继续拆成分目标，计划可挂在任意一级。" action={<button className="button button-primary" onClick={() => { reset(); setShowForm(true) }}><Plus size={16} />新建目标</button>}>
    {showForm && <form className="inline-editor" onSubmit={submit}><div><strong>{editing ? '编辑目标' : '新建目标'}</strong><span>父目标为空时创建根目标</span></div><input required placeholder="目标名称" value={title} onChange={(event) => setTitle(event.target.value)} /><input placeholder="完成标准" value={criteria} onChange={(event) => setCriteria(event.target.value)} /><select value={parentID} onChange={(event) => setParentID(event.target.value)}><option value="">根目标</option>{all.filter((item) => item.goal.id !== editing?.id).map(({ goal, depth }) => <option key={goal.id} value={goal.id}>{'　'.repeat(depth)}{goal.title}</option>)}</select><button className="button button-primary">保存</button><button className="button" type="button" onClick={reset}>取消</button></form>}
    {!data ? <LoadingBlock error={error} /> : <section className="goal-tree">{data.length ? data.map((goal) => <GoalBranch key={goal.id} goal={goal} depth={0} onEdit={beginEdit} onAdd={addChild} onComplete={complete} onAbandon={abandon} onOpenPlan={onOpenPlan} />) : <div className="empty-plans"><Flag size={26} /><strong>还没有目标</strong><span>先创建一个真正想达成的长期目标。</span></div>}</section>}
  </PageFrame>
}

function GoalBranch({ goal, depth, onEdit, onAdd, onComplete, onAbandon, onOpenPlan }: { goal: GoalNode; depth: number; onEdit: (goal: GoalNode) => void; onAdd: (goal: GoalNode) => void; onComplete: (goal: GoalNode) => void; onAbandon: (goal: GoalNode) => void; onOpenPlan: (id: string) => void }) {
  return <article className="goal-node" style={{ marginLeft: `${Math.min(depth, 5) * 24}px` }}><div className="goal-card"><span className={goal.status === 'achieved' ? 'goal-state achieved' : 'goal-state'}>{goal.status === 'achieved' ? <Check size={14} /> : <Target size={14} />}</span><div className="goal-copy"><strong>{goal.title}</strong><span>{goal.success_criteria || '尚未填写完成标准'}</span>{goal.ready_to_complete && <em>分目标和计划均已完成，可以检查目标</em>}</div><div className="goal-actions"><button onClick={() => onAdd(goal)}>新增分目标</button><button onClick={() => onEdit(goal)}>编辑</button>{goal.status === 'active' && <><button onClick={() => onComplete(goal)}>确认完成</button><button onClick={() => onAbandon(goal)}>放弃</button></>}</div></div>
    {goal.plans.length > 0 && <div className="goal-plans">{goal.plans.map((plan) => <button key={plan.id} onClick={() => onOpenPlan(plan.id)}><Route size={14} /><span>{plan.title}</span><em>{plan.done_tasks}/{plan.total_tasks}</em></button>)}</div>}
    {goal.children.map((child) => <GoalBranch key={child.id} goal={child} depth={depth + 1} onEdit={onEdit} onAdd={onAdd} onComplete={onComplete} onAbandon={onAbandon} onOpenPlan={onOpenPlan} />)}
  </article>
}

function PlansPage({ token, onOpen }: { token: string; onOpen: (id: string) => void }) {
  const plans = useLoad(() => api<PlanSummary[]>(token, '/plans'), [token])
  const goals = useLoad(() => api<GoalNode[]>(token, '/goals/tree'), [token])
  const [showForm, setShowForm] = useState(false)
  const [goalID, setGoalID] = useState('')
  const [title, setTitle] = useState('')
  const [mode, setMode] = useState<PlanMode>('calendar')
  const [capacity, setCapacity] = useState(600)
  const flat = flattenGoals(goals.data ?? [])
  useEffect(() => { if (!goalID && flat[0]) setGoalID(flat[0].goal.id) }, [goalID, flat])
  const create = async (event: FormEvent) => { event.preventDefault(); const value = await api<PlanDetail>(token, `/goals/${goalID}/plans`, { method: 'POST', body: JSON.stringify({ title, mode, weekly_capacity_minutes: capacity }) }); setShowForm(false); await plans.reload(); onOpen(value.plan.id) }
  return <PageFrame eyebrow="学习计划" title="多个计划可以同步进行" description="按日期安排到天，按课时计划依次推进。" action={<button className="button button-primary" onClick={() => setShowForm(true)} disabled={!flat.length}><Plus size={16} />新建计划</button>}>
    {showForm && <form className="inline-editor" onSubmit={create}><div><strong>创建计划草稿</strong><span>激活前可以完整编辑结构</span></div><input required placeholder="计划名称" value={title} onChange={(event) => setTitle(event.target.value)} /><select value={goalID} onChange={(event) => setGoalID(event.target.value)}>{flat.map(({ goal, depth }) => <option key={goal.id} value={goal.id}>{'　'.repeat(depth)}{goal.title}</option>)}</select><select value={mode} onChange={(event) => setMode(event.target.value as PlanMode)}><option value="calendar">按日期</option><option value="sequence">按课时</option></select><input type="number" min="0" value={capacity} onChange={(event) => setCapacity(Number(event.target.value))} aria-label="每周计划分钟数" /><button className="button button-primary">创建</button><button className="button" type="button" onClick={() => setShowForm(false)}>取消</button></form>}
    {!plans.data ? <LoadingBlock error={plans.error || goals.error} /> : <div className="plan-card-grid">{plans.data.map((plan) => <article className="plan-card" key={plan.id}><div className="plan-card-head"><span className="plan-type">{plan.mode === 'calendar' ? <CalendarDays size={13} /> : <BookOpen size={13} />}{plan.mode === 'calendar' ? '按日期' : '按课时'}</span><SourceBadge source={plan.source} /><span className={plan.active_version_id ? 'status-badge active' : 'status-badge draft'}>{plan.active_version_id ? `V${plan.active_version_no} 进行中` : '草稿'}</span></div><h2>{plan.title}</h2><p className="plan-description">{plan.description || '尚未填写计划说明。'}</p><div className="plan-progress-copy"><span>任务完成</span><strong>{plan.done_tasks} / {plan.total_tasks}</strong></div><div className="progress-track"><span style={{ width: `${plan.total_tasks ? plan.done_tasks / plan.total_tasks * 100 : 0}%` }} /></div>{plan.draft_version_id && <div className="plan-next"><span>待处理</span><strong>存在尚未激活的新版本草稿</strong></div>}<button className="button plan-open-button" onClick={() => onOpen(plan.id)}>查看计划 <ChevronRight size={15} /></button></article>)}</div>}
  </PageFrame>
}

function PlanDetailPage({ token, planID, onOpenTask }: { token: string; planID: string; onOpenTask: (task: Task, reload: () => void) => void }) {
  const { data, error, reload } = useLoad(() => api<PlanDetail>(token, `/plans/${planID}`), [token, planID])
  const [selectedID, setSelectedID] = useState('')
  const [milestoneTitle, setMilestoneTitle] = useState('')
  const [taskTitle, setTaskTitle] = useState('')
  const [taskMilestone, setTaskMilestone] = useState('')
  const [taskDate, setTaskDate] = useState('')
  useEffect(() => { if (data) { const preferred = data.versions.find((version) => version.status === 'draft') ?? data.versions.find((version) => version.status === 'active') ?? data.versions[0]; setSelectedID(preferred?.id ?? '') } }, [data])
  if (!data) return <PageFrame eyebrow="计划详情" title="读取计划" description="正在加载版本和任务树。"><LoadingBlock error={error} /></PageFrame>
  const selected = data.versions.find((version) => version.id === selectedID) ?? data.versions[0]
  const addMilestone = async (event: FormEvent) => { event.preventDefault(); await api(token, `/plan-versions/${selected.id}/milestones`, { method: 'POST', body: JSON.stringify({ title: milestoneTitle }) }); setMilestoneTitle(''); await reload() }
  const addTask = async (event: FormEvent) => { event.preventDefault(); await api(token, `/plan-versions/${selected.id}/tasks`, { method: 'POST', body: JSON.stringify({ title: taskTitle, milestone_id: taskMilestone || undefined, scheduled_date: data.plan.mode === 'calendar' && taskDate ? taskDate : undefined, estimate_minutes: 60 }) }); setTaskTitle(''); setTaskDate(''); await reload() }
  const activate = async () => { await api(token, `/plan-versions/${selected.id}/activate`, { method: 'POST' }); await reload() }
  const clone = async () => { const version = await api<PlanVersion>(token, `/plans/${data.plan.id}/versions`, { method: 'POST' }); await reload(); setSelectedID(version.id) }
  return <PageFrame eyebrow={data.plan.mode === 'calendar' ? '按日期计划' : '按课时计划'} title={data.plan.title} description={data.plan.description || '通过版本草稿调整结构，激活后进入执行。'} action={<div className="topbar-actions">{data.plan.active_version_id && !data.versions.some((version) => version.status === 'draft') && <button className="button" onClick={clone}><Plus size={15} />创建新版本</button>}{selected?.status === 'draft' && <button className="button button-primary" onClick={activate}>激活 V{selected.version_no}</button>}</div>}>
    <div className="version-tabs">{data.versions.map((version) => <button key={version.id} className={version.id === selected.id ? 'is-current' : ''} onClick={() => setSelectedID(version.id)}>V{version.version_no}<span>{version.status === 'draft' ? '草稿' : version.status === 'active' ? '生效中' : '历史'}</span></button>)}</div>
    {selected.status === 'draft' && <form className="quick-add" onSubmit={addMilestone}><div><strong>新增阶段</strong><span>把计划拆成有明确结果的阶段</span></div><input required value={milestoneTitle} onChange={(event) => setMilestoneTitle(event.target.value)} placeholder="阶段名称" /><span /><button className="button button-primary">添加</button></form>}
    {(selected.status === 'draft' || selected.status === 'active') && <form className="quick-add" onSubmit={addTask}><div><strong>新增任务</strong><span>{selected.status === 'active' ? '新任务会立即进入当前计划' : '任务可暂时不归入阶段'}</span></div><input required value={taskTitle} onChange={(event) => setTaskTitle(event.target.value)} placeholder="任务名称" /><select value={taskMilestone} onChange={(event) => setTaskMilestone(event.target.value)}><option value="">未分阶段</option>{selected.milestones.map((item) => <option key={item.id} value={item.id}>{item.title}</option>)}</select>{data.plan.mode === 'calendar' && <input type="date" value={taskDate} onChange={(event) => setTaskDate(event.target.value)} />}<button className="button button-primary">添加</button></form>}
    <section className="detail-layout"><div className="detail-main"><Unassigned tasks={selected.unassigned_tasks} onOpen={(task) => onOpenTask({ ...task, plan_id: data.plan.id, plan_title: data.plan.title, plan_mode: data.plan.mode, version_status: selected.status }, reload)} /><div className="stage-list">{selected.milestones.map((milestone) => <MilestoneCard key={milestone.id} milestone={milestone} onOpen={(task) => onOpenTask({ ...task, plan_id: data.plan.id, plan_title: data.plan.title, plan_mode: data.plan.mode, version_status: selected.status }, reload)} />)}</div></div><aside className="detail-aside"><section><h3>版本信息</h3><dl><div><dt>版本</dt><dd>V{selected.version_no}</dd></div><div><dt>状态</dt><dd>{selected.status}</dd></div><div><dt>每周投入</dt><dd>{minutes(selected.weekly_capacity_minutes)}</dd></div></dl></section></aside></section>
  </PageFrame>
}

function Unassigned({ tasks, onOpen }: { tasks: Task[]; onOpen: (task: Task) => void }) { if (!tasks.length) return null; return <section className="stage-card"><div className="stage-head"><div><span>—</span><strong>未分阶段</strong></div><div><span>{tasks.length} 项</span></div></div><div className="stage-tasks">{tasks.map((task) => <StageTask key={task.id} task={task} onOpen={onOpen} />)}</div></section> }
function MilestoneCard({ milestone, onOpen }: { milestone: Milestone; onOpen: (task: Task) => void }) { return <section className="stage-card"><div className="stage-head"><div><span>{String(milestone.position).padStart(2, '0')}</span><strong>{milestone.title}</strong></div><div><span>{milestone.tasks.filter((task) => task.status === 'done').length}/{milestone.tasks.length}</span></div></div>{milestone.outcome && <p className="stage-outcome">{milestone.outcome}</p>}<div className="stage-tasks">{milestone.tasks.map((task) => <StageTask key={task.id} task={task} onOpen={onOpen} />)}</div></section> }
function StageTask({ task, onOpen }: { task: Task; onOpen: (task: Task) => void }) { return <button className={task.status === 'done' ? 'stage-task is-done' : 'stage-task'} onClick={() => onOpen(task)}><span>{task.status === 'done' ? <Check size={13} /> : <Circle size={14} />}</span><strong>{task.title}</strong><em>{task.status === 'done' ? '已完成' : task.scheduled_date ?? '待执行'}</em></button> }

function TasksPage({ token, onOpen }: { token: string; onOpen: (task: Task, reload: () => void) => void }) {
  const { data, error, reload } = useLoad(() => api<Task[]>(token, '/tasks'), [token])
  const [query, setQuery] = useState('')
  const [status, setStatus] = useState('')
  const visible = (data ?? []).filter((task) => task.title.toLowerCase().includes(query.toLowerCase()) && (!status || task.status === status))
  return <PageFrame eyebrow="任务中心" title="全部任务" description="统一查询所有生效计划中的任务。"><section className="task-toolbar"><label className="search-box"><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索任务" /></label><select value={status} onChange={(event) => setStatus(event.target.value)}><option value="">全部状态</option><option value="todo">待完成</option><option value="in_progress">进行中</option><option value="done">已完成</option></select><span /></section>{!data ? <LoadingBlock error={error} /> : <section className="task-table-panel">{visible.map((task) => <TaskRow key={task.id} task={task} onOpen={(value) => onOpen(value, reload)} onComplete={async (value) => { await api(token, `/tasks/${value.id}/${value.status === 'done' ? 'reopen' : 'complete'}`, { method: 'POST' }); await reload() }} />)}{!visible.length && <div className="empty-row">没有符合条件的任务</div>}</section>}</PageFrame>
}

function ReviewPage({ token }: { token: string }) {
  const { data, error } = useLoad(() => api<WeeklyReview>(token, '/reviews/weekly'), [token])
  if (!data) return <PageFrame eyebrow="学习复盘" title="本周执行情况" description="只展示真实任务和计时记录。"><LoadingBlock error={error} /></PageFrame>
  const max = Math.max(1, ...data.daily.map((item) => item.duration_seconds))
  return <PageFrame eyebrow={`${data.week_start} — ${data.week_end}`} title="本周学习复盘" description="比较计划投入与实际执行，不直接推断能力是否掌握。">
    <section className="review-metrics"><div><span>完成任务</span><strong>{data.completed_tasks}</strong><em>本周完成</em></div><div><span>实际投入</span><strong>{hours(data.actual_duration_seconds)}</strong><em>计划 {minutes(data.planned_minutes)}</em></div><div><span>按期完成率</span><strong>{Math.round(data.on_time_rate * 100)}%</strong><em>{data.on_time_completed}/{data.scheduled_tasks} 项</em></div><div><span>当前逾期</span><strong>{data.current_overdue}</strong><em>{data.reschedule_count} 次改期</em></div></section>
    <div className="review-grid"><section className="review-card weekly-chart"><div className="review-card-head"><div><h2>每日投入</h2><p>按计时开始日期统计</p></div><strong>{hours(data.actual_duration_seconds)}</strong></div><div className="bar-chart">{data.daily.map((item) => <div className="bar-column" key={item.date}><span className="bar-value">{hours(item.duration_seconds)}</span><i style={{ height: `${item.duration_seconds / max * 100}%` }} /><em>{item.date.slice(5)}</em></div>)}</div></section><section className="review-card"><div className="review-card-head"><div><h2>各计划投入</h2><p>计划预算与实际计时</p></div></div><div className="distribution-list">{data.plans.map((plan) => <div key={plan.plan_id}><span>{plan.plan_title}</span><strong>{minutes(plan.planned_minutes)} / {hours(plan.actual_duration_seconds)}</strong></div>)}</div></section></div>
    <section className="review-card plan-review"><div className="review-card-head"><div><h2>各计划执行情况</h2><p>完成数、实际投入和改期记录</p></div></div><div className="plan-review-head"><span>计划</span><span>完成任务</span><span>计划 / 实际</span><span>改期</span><span>状态</span></div>{data.plans.map((plan) => <div className="plan-review-row" key={plan.plan_id}><strong>{plan.plan_title}</strong><span>{plan.completed_tasks}</span><span>{minutes(plan.planned_minutes)} / {hours(plan.actual_duration_seconds)}</span><span>{plan.reschedule_count} 次</span><span>{plan.actual_duration_seconds >= plan.planned_minutes * 60 ? '达到投入' : '继续推进'}</span></div>)}</section>
  </PageFrame>
}

function SettingsPage({ token, account, onLogout }: { token: string; account: Account; onLogout: () => void }) {
  const { data, error, reload } = useLoad(() => api<Preferences>(token, '/preferences'), [token])
  const save = async (changes: Partial<Preferences>) => { await api(token, '/preferences', { method: 'PATCH', body: JSON.stringify(changes) }); await reload() }
  return <PageFrame eyebrow="个人空间" title="设置" description="管理计划偏好和当前账号。">
    <section className="settings-card account-card"><div className="settings-title"><span className="settings-icon"><ShieldCheck size={18} /></span><div><h2>共享认证账号</h2><p>StudyFlow 使用 shared-auth 提供的 UUID 身份。</p></div></div><div className="account-profile"><span className="large-avatar">{account.identities[0]?.value.slice(0, 1).toUpperCase() ?? 'U'}</span><div><strong>{account.identities[0]?.value ?? account.user.id}</strong><span>邮箱已验证 · {account.user.id}</span></div><button className="button" onClick={onLogout}><LogOut size={15} />退出登录</button></div></section>
    {!data ? <LoadingBlock error={error} /> : <section className="settings-card"><div className="settings-title"><span className="settings-icon"><Settings size={18} /></span><div><h2>计划偏好</h2><p>用于首页日期和周度复盘。</p></div></div><div className="setting-row"><div><strong>每周开始日</strong><span>决定周报统计范围</span></div><select value={data.week_start} onChange={(event) => save({ week_start: event.target.value as Preferences['week_start'] })}><option value="monday">星期一</option><option value="sunday">星期日</option></select></div><div className="setting-row"><div><strong>默认计划方式</strong><span>新建计划时的默认选择</span></div><select value={data.default_plan_mode} onChange={(event) => save({ default_plan_mode: event.target.value as PlanMode })}><option value="calendar">按日期</option><option value="sequence">按课时</option></select></div><div className="setting-row"><div><strong>首页显示已完成任务</strong><span>当天完成后继续保留</span></div><button className={data.show_completed_today ? 'switch is-on' : 'switch'} onClick={() => save({ show_completed_today: !data.show_completed_today })}><span /></button></div></section>}
  </PageFrame>
}

const sampleDraft = `{
  "title": "示例：三周复习计划",
  "description": "由 JobPilot 生成、用户确认后导入。",
  "mode": "calendar",
  "weekly_capacity_minutes": 300,
  "milestones": [
    {
      "title": "第一阶段 · 基础",
      "outcome": "把模板过一遍",
      "tasks": [
        { "title": "任务一", "estimate_minutes": 45, "scheduled_date": "2026-10-01" },
        { "title": "任务二", "estimate_minutes": 30 }
      ]
    },
    { "title": "第二阶段 · 进阶", "tasks": [{ "title": "任务三", "estimate_minutes": 60 }] }
  ],
  "tasks": [{ "title": "未分阶段任务", "estimate_minutes": 20 }]
}`

// crypto.randomUUID only exists in a secure context, and the container
// deployment serves plain HTTP on a LAN address, so fall back to something that
// still satisfies the server's 8-200 printable ASCII rule.
function newIdempotencyKey() {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return `ui-${crypto.randomUUID()}`
  return `ui-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 12)}`
}

function SourceBadge({ source }: { source: TaskSource }) {
  if (source !== 'agent') return null
  return <span className="source-badge" title="由 Agent 通过 MCP 导入">Agent 导入</span>
}

function ImportsPage({ token, onOpenPlan }: { token: string; onOpenPlan: (id: string) => void }) {
  const goals = useLoad(() => api<GoalNode[]>(token, '/goals/tree'), [token])
  const history = useLoad(() => api<PlanImportView[]>(token, '/plan-imports'), [token])
  const [goalID, setGoalID] = useState('')
  const [draft, setDraft] = useState('')
  const [key, setKey] = useState('')
  const [result, setResult] = useState<PlanImportResult | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const all = flattenGoals(goals.data ?? [])
  useEffect(() => { if (!goalID && all[0]) setGoalID(all[0].goal.id) }, [goalID, all])

  // 改草稿或换目标都作废幂等键：同一份输入再点一次确认是重放，输入变了才算新的一次导入。
  const editDraft = (value: string) => { setDraft(value); setKey(''); setResult(null); setError('') }
  const changeGoal = (value: string) => { setGoalID(value); setKey(''); setResult(null); setError('') }

  let preview: { title: string; mode: string; milestones: number; tasks: number } | null = null
  let parseError = ''
  if (draft.trim()) {
    try {
      const value = JSON.parse(draft) as ImportDraft
      const milestones = Array.isArray(value.milestones) ? value.milestones : []
      const loose = Array.isArray(value.tasks) ? value.tasks : []
      preview = {
        title: (value.title ?? '').trim() || '（未命名）',
        mode: value.mode === 'sequence' ? '按课时' : '按日期',
        milestones: milestones.length,
        tasks: milestones.reduce((sum, item) => sum + (Array.isArray(item?.tasks) ? item.tasks.length : 0), 0) + loose.length,
      }
    } catch { parseError = '草稿不是合法的 JSON。' }
  }

  const confirm = async () => {
    if (!preview) return
    setBusy(true); setError('')
    try {
      const body = JSON.parse(draft) as ImportDraft
      // Mint the key and store it before sending: if the response is lost the next
      // click has to reuse it, otherwise the retry would import a second copy —
      // exactly what the key exists to prevent.
      const idempotencyKey = key || newIdempotencyKey()
      setKey(idempotencyKey)
      const value = await api<PlanImportResult>(token, '/plan-imports', {
        method: 'POST',
        headers: { 'Idempotency-Key': idempotencyKey },
        body: JSON.stringify({ ...body, goal_id: goalID }),
      })
      setResult(value)
      await history.reload()
    } catch (reason) { setError(messageOf(reason)) } finally { setBusy(false) }
  }

  return <PageFrame eyebrow="计划导入" title="确认之后才会写进去" description="粘贴 JobPilot 生成的学习路线草稿，核对无误再导入。导入会落在草稿版本上，激活后才进入执行。">
    <section className="import-layout">
      <div className="import-form">
        <label><span>导入到哪个目标</span>
          <select value={goalID} onChange={(event) => changeGoal(event.target.value)} disabled={!all.length}>
            {all.map(({ goal, depth }) => <option key={goal.id} value={goal.id}>{'　'.repeat(depth)}{goal.title}</option>)}
          </select>
        </label>
        <label><span>路线草稿（JSON）</span>
          <textarea value={draft} onChange={(event) => editDraft(event.target.value)} rows={16} spellCheck={false} placeholder='{"title": "…", "mode": "calendar", "milestones": []}' />
        </label>
        <div className="import-actions">
          <button className="button" type="button" onClick={() => editDraft(sampleDraft)}>填入示例</button>
          <button className="button" type="button" onClick={() => editDraft('')} disabled={!draft}>清空</button>
          <span />
          <button className="button button-primary" onClick={confirm} disabled={!preview || busy || !goalID}>{busy ? '导入中…' : key ? '再次确认（会重放）' : '确认导入'}</button>
        </div>
        {parseError && <p className="import-error">{parseError}</p>}
        {error && <p className="import-error">{error}</p>}
      </div>
      <aside className="import-side">
        <section className="import-card">
          <h2>将创建的内容</h2>
          {!preview ? <p className="muted-copy">粘贴草稿后这里会显示摘要。</p> : <dl className="import-summary">
            <div><dt>计划</dt><dd>{preview.title}</dd></div>
            <div><dt>模式</dt><dd>{preview.mode}</dd></div>
            <div><dt>阶段</dt><dd>{preview.milestones}</dd></div>
            <div><dt>任务</dt><dd>{preview.tasks}</dd></div>
          </dl>}
        </section>
        <section className="import-card">
          <h2>导入记录</h2>
          {!history.data?.length ? <p className="muted-copy">还没有导入过任何计划。</p> : <ul className="import-history">
            {history.data.map((item) => <li key={item.import_id}>
              <button type="button" onClick={() => onOpenPlan(item.plan_id)}>
                <strong>{item.plan_title || '（计划已不存在）'}</strong>
                <span>{new Date(item.created_at).toLocaleString('zh-CN')}</span>
                <em>{item.milestones} 阶段 / {item.tasks} 任务</em>
              </button>
            </li>)}
          </ul>}
        </section>
        <section className="import-card">
          <h2>幂等</h2>
          <p className="muted-copy">{key ? '这份草稿已经带着一个幂等键。再点一次确认只会返回上次的结果，不会重复建；改动草稿会换一个新键。' : '第一次确认时会生成幂等键，所以超时或重复点击都不会建出两份。'}</p>
        </section>
        {result && <section className="import-card import-result">
          <h2>{result.replayed ? '已存在，返回上次结果' : '导入成功'}</h2>
          <p className="muted-copy">{result.replayed ? '这次没有新建任何数据。' : `版本状态 ${result.plan.versions[0]?.status ?? 'draft'}，激活后进入执行。`}</p>
          <button className="button plan-open-button" onClick={() => onOpenPlan(result.plan_id)}>打开计划 <ChevronRight size={15} /></button>
        </section>}
      </aside>
    </section>
  </PageFrame>
}

function TaskDrawer({ token, task, running, onClose, onChanged }: { token: string; task: Task; running: Session | null; onClose: () => void; onChanged: () => void }) {
  const history = useLoad(() => api<Session[]>(token, `/tasks/${task.id}/sessions`), [token, task.id])
  const [title, setTitle] = useState(task.title)
  const [description, setDescription] = useState(task.description)
  const [date, setDate] = useState(task.scheduled_date ?? '')
  const [estimate, setEstimate] = useState(task.estimate_minutes)
  const [sessionNote, setSessionNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const execute = async (job: () => Promise<unknown>) => { setBusy(true); setNotice(''); try { await job(); onChanged() } catch (reason) { setNotice(messageOf(reason)) } finally { setBusy(false) } }
  const save = () => execute(() => api(token, `/tasks/${task.id}`, { method: 'PATCH', body: JSON.stringify({ version: task.version, title, description, estimate_minutes: estimate, scheduled_date: date || undefined, clear_scheduled_date: !date }) }))
  const isRunning = running?.task_id === task.id
  const isDraft = task.version_status === 'draft'
  return <div className="drawer-backdrop" onMouseDown={(event) => event.target === event.currentTarget && onClose()}><aside className="task-drawer"><div className="drawer-head"><div><span>任务详情</span><strong>{task.plan_title || '学习计划'}</strong></div><button onClick={onClose}><X size={18} /></button></div><div className="drawer-body"><div className="drawer-status-row"><span className="plan-badge tone-blue">{task.plan_mode === 'sequence' ? '按课时' : '按日期'}</span><span className={task.is_overdue ? 'task-state is-overdue' : task.status === 'done' ? 'task-state is-done' : 'task-state'}>{isDraft ? '草稿任务' : task.is_overdue ? '已逾期' : task.status}</span></div><input className="drawer-title-input" value={title} onChange={(event) => setTitle(event.target.value)} /><div className="drawer-fields"><label><span>安排日期</span><input type="date" disabled={task.plan_mode === 'sequence'} value={date} onChange={(event) => setDate(event.target.value)} /></label><label><span>预计投入（分钟）</span><input type="number" min="0" value={estimate} onChange={(event) => setEstimate(Number(event.target.value))} /></label><label><span>版本</span><strong>{task.version}</strong></label><label><span>状态</span><strong>{isDraft ? '尚未激活' : task.status}</strong></label></div><section className="drawer-section"><h3>任务说明</h3><textarea value={description} onChange={(event) => setDescription(event.target.value)} /></section>{!isDraft && <section className="drawer-section"><h3>执行计时</h3>{isRunning ? <><div className="activity-row"><span className="activity-dot active-dot" /><div><strong>正在计时</strong><span>开始于 {new Date(running.started_at).toLocaleString('zh-CN')}</span></div></div><textarea className="session-note" value={sessionNote} onChange={(event) => setSessionNote(event.target.value)} placeholder="结束计时时保存一条简短记录（可选）" /></> : <p>开始后由服务器记录实际投入时间，同一时间只能计时一项任务。</p>}<div className="session-history">{history.error && <span className="drawer-error">{history.error}</span>}{history.data?.map((session) => <div className="session-record" key={session.id}><span className={session.status === 'finished' ? 'activity-dot' : 'activity-dot muted-dot'} /><div><strong>{session.status === 'finished' ? `学习 ${Math.max(1, Math.round(session.duration_seconds / 60))} 分钟` : session.status === 'running' ? '正在计时' : '已丢弃'}</strong><span>{new Date(session.started_at).toLocaleString('zh-CN')}{session.note ? ` · ${session.note}` : ''}</span></div></div>)}{history.data && !history.data.length && <span className="muted-copy">暂无历史计时记录</span>}</div></section>}{notice && <p className="drawer-error">{notice}</p>}</div><div className="drawer-actions"><button className="button" disabled={busy} onClick={save}>保存修改</button>{isDraft ? <button className="button button-danger" disabled={busy} onClick={() => execute(() => api(token, `/tasks/${task.id}`, { method: 'DELETE' }))}>删除草稿任务</button> : <>{task.status === 'done' ? <button className="button" disabled={busy} onClick={() => execute(() => api(token, `/tasks/${task.id}/reopen`, { method: 'POST' }))}>重新打开</button> : task.status !== 'canceled' && <button className="button" disabled={busy} onClick={() => execute(() => api(token, `/tasks/${task.id}/complete`, { method: 'POST' }))}>完成任务</button>}{task.status !== 'done' && task.status !== 'canceled' && !isRunning && <button className="button button-danger" disabled={busy} onClick={() => execute(() => api(token, `/tasks/${task.id}/cancel`, { method: 'POST' }))}>取消任务</button>}{isRunning ? <><button className="button" disabled={busy} onClick={() => execute(() => api(token, `/sessions/${running.id}/discard`, { method: 'POST' }))}>丢弃计时</button><button className="button button-primary" disabled={busy} onClick={() => execute(() => api(token, `/sessions/${running.id}/finish`, { method: 'POST', body: JSON.stringify({ note: sessionNote }) }))}><Clock3 size={15} />结束计时</button></> : <button className="button button-primary" disabled={busy || task.status === 'done' || task.status === 'canceled'} onClick={() => execute(() => api(token, `/tasks/${task.id}/sessions`, { method: 'POST' }))}><Timer size={15} />开始计时</button>}</>}</div></aside></div>
}

export default function App() {
  const [token, setToken] = useState('')
  const [account, setAccount] = useState<Account | null>(null)
  const [booting, setBooting] = useState(true)
  const [page, setPage] = useState<Page>('today')
  const [planID, setPlanID] = useState('')
  const [selectedTask, setSelectedTask] = useState<Task | null>(null)
  const [drawerReload, setDrawerReload] = useState<(() => void) | null>(null)
  const [running, setRunning] = useState<Session | null>(null)
  const [notice, setNotice] = useState('')

  const accept = useCallback((issue: { access_token: string; account: Account }) => { setToken(issue.access_token); setAccount(issue.account) }, [])
  useEffect(() => { refreshAccess().then(accept).catch(() => undefined).finally(() => setBooting(false)) }, [accept])
  useEffect(() => { if (!token) return; const timer = window.setInterval(() => refreshAccess().then(accept).catch(() => { setToken(''); setAccount(null) }), 12 * 60 * 1000); return () => window.clearInterval(timer) }, [token, accept])
  const openTask = (task: Task, reload: () => void) => { setSelectedTask(task); setDrawerReload(() => reload); void api<Dashboard>(token, '/dashboard/today').then((value) => setRunning(value.running_session?.session ?? null)) }
  const changed = async () => { setSelectedTask(null); setRunning(null); drawerReload?.(); setNotice('操作已保存。') }
  const openPlan = (id: string) => { setPlanID(id); setPage('plan-detail') }
  const signOut = async () => { await logout(); setToken(''); setAccount(null) }
  if (booting) return <main className="auth-shell"><div className="auth-card"><strong>正在恢复登录状态…</strong></div></main>
  if (!token || !account) return <AuthPage onAuthenticated={accept} />
  const title = page === 'today' ? ['今天', new Date().toLocaleDateString('zh-CN', { month: 'long', day: 'numeric', weekday: 'long' })] : page === 'goals' ? ['目标', '递归拆解长期目标'] : page === 'plans' ? ['学习计划', '多个计划同步推进'] : page === 'plan-detail' ? ['计划详情', '版本、阶段与任务'] : page === 'tasks' ? ['全部任务', '查询所有生效计划'] : page === 'reviews' ? ['学习复盘', '本周执行情况'] : page === 'imports' ? ['计划导入', '确认后写入'] : ['设置', '账号与计划偏好']
  return <div className="app-shell"><aside className="sidebar"><div className="brand"><span className="brand-mark">S</span><strong>StudyFlow</strong></div><nav className="navigation">{nav.map((item) => { const Icon = item.icon; return <button key={item.id} className={page === item.id || (page === 'plan-detail' && item.id === 'plans') ? 'nav-button is-current' : 'nav-button'} onClick={() => { setPage(item.id); setNotice('') }}><Icon size={18} /><span>{item.label}</span></button> })}</nav><div className="sidebar-footer"><span className="user-avatar">{account.identities[0]?.value.slice(0, 1).toUpperCase() ?? 'U'}</span><div><strong>{account.identities[0]?.value ?? '已登录用户'}</strong><span>个人空间</span></div><ChevronDown size={16} /></div></aside><main className="main-area"><header className="topbar"><div><span>{title[0]}</span><strong>{title[1]}</strong></div></header>{notice && <div className="notice"><span>{notice}</span><button onClick={() => setNotice('')}>×</button></div>}{page === 'today' && <TodayPage token={token} onOpen={openTask} />}{page === 'goals' && <GoalsPage token={token} onOpenPlan={openPlan} />}{page === 'plans' && <PlansPage token={token} onOpen={openPlan} />}{page === 'plan-detail' && planID && <PlanDetailPage token={token} planID={planID} onOpenTask={openTask} />}{page === 'tasks' && <TasksPage token={token} onOpen={openTask} />}{page === 'reviews' && <ReviewPage token={token} />}{page === 'settings' && <SettingsPage token={token} account={account} onLogout={signOut} />}{page === 'imports' && <ImportsPage token={token} onOpenPlan={openPlan} />}</main>{selectedTask && <TaskDrawer token={token} task={selectedTask} running={running} onClose={() => setSelectedTask(null)} onChanged={changed} />}</div>
}
