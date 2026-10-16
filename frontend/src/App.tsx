import { FormEvent, ReactNode, useCallback, useEffect, useRef, useState } from 'react'
import {
  AlertCircle, BarChart3, BookOpen, CalendarDays, Check, ChevronDown, ChevronRight,
  Circle, Flag, Home, ListChecks, LogOut, Plus, RefreshCcw, Route, Settings,
  ShieldCheck, Target, Timer,
} from 'lucide-react'
import { Account, APIError, api, login, logout, refreshAccess, register, requestVerification } from './api'
import { useLoad } from './data'
import { SyncProvider, SyncStatus, clearSyncCursor } from './sync'
import TaskDrawer from './TaskDrawer'
import { GoalEditor, PlanCreator, VersionEditor } from './CreationDialogs'
import PlanOutline from './PlanOutline'
import type { Dashboard, GoalNode, ImportDraft, PlanDetail, PlanImportResult, PlanImportView, PlanMode, PlanSummary, PlanVersion, Preferences, Session, Task, TaskSource, WeeklyReview } from './types'

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
  const { data, error, reload } = useLoad(() => api<Dashboard>(token, '/dashboard/today'), [token], ['dashboard'])
  const setDone = async (task: Task) => { await api(token, `/tasks/${task.id}/${task.status === 'done' ? 'reopen' : 'complete'}`, { method: 'POST' }); await reload() }
  if (!data) return <PageFrame eyebrow="今日工作台" title="先处理最需要推进的内容" description="日期任务安排到天，课时计划按顺序继续。"><LoadingBlock error={error} /></PageFrame>
  return <PageFrame eyebrow={data.date} title="先处理最需要推进的内容" description="多个计划可以同步进行，计时记录会进入本周复盘。">
    <section className="summary-strip"><div><strong>{data.overdue.length}</strong><span>已逾期</span></div><div><strong>{data.today.filter((task) => task.status !== 'done').length}</strong><span>今天待完成</span></div><div><strong>{data.sequence_plans.length}</strong><span>课时计划进行中</span></div><div className="summary-copy"><CalendarDays size={17} /><span>未来任务按日期依次排列</span></div></section>
    {data.running_session && <section className="running-banner"><Timer size={18} /><div><strong>正在计时：{data.running_session.task.title}</strong><span>开始于 {new Date(data.running_session.session.started_at).toLocaleTimeString('zh-CN')}</span></div><button className="button" onClick={() => onOpen(data.running_session!.task, reload)}>查看</button></section>}
    <Agenda title="已逾期" description="先完成、改期或取消，避免任务持续堆积。" danger count={data.overdue.length}>{data.overdue.map((task) => <TaskRow key={task.id} task={task} onOpen={(value) => onOpen(value, reload)} onComplete={setDone} />)}</Agenda>
    <Agenda title="今天" description="这里只安排日期，不规定几点开始。" count={data.today.length}>{data.today.map((task) => <TaskRow key={task.id} task={task} onOpen={(value) => onOpen(value, reload)} onComplete={setDone} />)}</Agenda>
    <Agenda title="今日已完成" description="按完成时间统计，昨天该做、今天才做完的也算在这里。" count={data.completed_today.length}>{data.completed_today.map((task) => <TaskRow key={task.id} task={task} onOpen={(value) => onOpen(value, reload)} onComplete={setDone} />)}</Agenda>
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

function completionWarning(reason: unknown, scope: 'goal' | 'plan') {
  if (!(reason instanceof APIError) || reason.code !== 'completion_confirmation_required') return false
  const summary = (reason.data ?? {}) as Record<string, number | boolean>
  const detail = scope === 'goal'
    ? `仍有 ${summary.incomplete_children ?? 0} 个直属子项未达成。`
    : summary.has_active_version
      ? `当前版本还有 ${summary.open_tasks ?? 0} 个任务未完成。`
      : '这份计划还没有生效版本。'
  return window.confirm(`${detail}\n\n系统只提供进度提醒。是否仍确认目标已经达成？`)
}

function chooseParentEffect(label: string) {
  const value = window.prompt(`放弃“${label}”后如何影响上层？\n输入 block：继续阻塞上层完成\n输入 exclude：从上层判断中排除`, 'block')
  return value === 'block' || value === 'exclude' ? value : null
}

function GoalsPage({ token, onOpenPlan }: { token: string; onOpenPlan: (id: string) => void }) {
  const { data, error, reload } = useLoad(() => api<GoalNode[]>(token, '/goals/tree'), [token], ['goals'])
  const [editor, setEditor] = useState<{ initial?: GoalNode; parentID?: string } | null>(null)
  const [planGoalID, setPlanGoalID] = useState<string | null>(null)
  const [notice, setNotice] = useState('')
  const run = async (job: () => Promise<unknown>) => { setNotice(''); try { await job(); await reload() } catch (reason) { setNotice(messageOf(reason)) } }
  const complete = (goal: GoalNode) => run(async () => {
    try { await api(token, `/goals/${goal.id}/complete`, { method: 'POST' }) }
    catch (reason) {
      if (!completionWarning(reason, 'goal')) { setNotice(messageOf(reason)); return }
      await api(token, `/goals/${goal.id}/complete`, { method: 'POST', body: JSON.stringify({ acknowledge_incomplete: true }) })
    }
  })
  const abandon = async (goal: GoalNode) => {
    const effect = goal.parent_goal_id ? chooseParentEffect(goal.title) : ''
    if (goal.parent_goal_id && !effect) return
    await run(() => api(token, `/goals/${goal.id}/abandon`, { method: 'POST', body: JSON.stringify({ parent_effect: effect }) }))
  }
  const reopen = (goal: GoalNode) => run(() => api(token, `/goals/${goal.id}/reopen`, { method: 'POST' }))
  const togglePolicy = (goal: GoalNode) => run(() => api(token, `/goals/${goal.id}/completion-policy`, { method: 'PATCH', body: JSON.stringify({ parent_effect: goal.excluded_from_parent_completion ? 'block' : 'exclude' }) }))
  return <PageFrame eyebrow="目标" title="想达成什么，从这里开始" description="先写下目标，再拆分目标或添加学习计划，逐步落实到行动。" action={<button className="button button-primary" onClick={() => setEditor({})}><Plus size={16} />新建目标</button>}>
    {notice && <p role="alert" className="drawer-error">{notice}</p>}
    {!data ? <LoadingBlock error={error} /> : <>
      <div className="goal-explainer"><Target size={19} /><p><strong>目标是想达成的结果，计划是具体的实现路线。</strong><span>大目标可以继续拆分；已有明确行动时，直接在目标下添加计划。</span></p></div>
      <section className="goal-tree">{data.length ? data.map(goal => <GoalBranch key={goal.id} goal={goal} depth={0} onEdit={goal => setEditor({ initial: goal })} onAdd={goal => setEditor({ parentID: goal.id })} onAddPlan={goal => setPlanGoalID(goal.id)} onComplete={complete} onAbandon={abandon} onReopen={reopen} onTogglePolicy={togglePolicy} onOpenPlan={onOpenPlan} />) : <div className="empty-plans"><Flag size={28} /><strong>从一个你想达成的目标开始</strong><span>例如：能独立开发并上线一个后端服务。</span><button className="button" onClick={() => setEditor({})}>创建第一个目标</button></div>}</section>
    </>}
    {editor && <GoalEditor token={token} goals={data ?? []} {...editor} onClose={() => setEditor(null)} onSaved={() => { setEditor(null); void reload() }} />}
    {planGoalID !== null && <PlanCreator token={token} goals={data ?? []} initialGoalID={planGoalID} onClose={() => setPlanGoalID(null)} onCreated={onOpenPlan} onGoalsChanged={() => { void reload() }} />}
  </PageFrame>
}

function GoalBranch({ goal, depth, onEdit, onAdd, onAddPlan, onComplete, onAbandon, onReopen, onTogglePolicy, onOpenPlan }: {
  goal: GoalNode; depth: number; onEdit: (goal: GoalNode) => void; onAdd: (goal: GoalNode) => void; onAddPlan: (goal: GoalNode) => void; onComplete: (goal: GoalNode) => void; onAbandon: (goal: GoalNode) => void; onReopen: (goal: GoalNode) => void; onTogglePolicy: (goal: GoalNode) => void; onOpenPlan: (id: string) => void
}) {
  const [expanded, setExpanded] = useState(true)
  const hasChildren = goal.children.length > 0 || goal.plans.length > 0
  return <article className="goal-node" style={{ marginLeft: depth ? '20px' : 0 }}>
    <div className="goal-card">
      <button className={goal.status === 'achieved' ? 'goal-state achieved' : 'goal-state'} aria-label={`${expanded ? '收起' : '展开'} ${goal.title}`} aria-expanded={expanded} disabled={!hasChildren} onClick={() => setExpanded(value => !value)}>{hasChildren ? expanded ? <ChevronDown size={16} /> : <ChevronRight size={16} /> : goal.status === 'achieved' ? <Check size={16} /> : <Target size={16} />}</button>
      <div className="goal-copy"><strong>{goal.title}</strong><span>{goal.status === 'active' ? '进行中' : goal.status === 'achieved' ? '已达成' : goal.excluded_from_parent_completion ? '已放弃（不计入上层）' : '已放弃（仍阻塞上层）'}{goal.target_date && ` · 目标日期 ${goal.target_date}`}</span>{goal.success_criteria && <p>{goal.success_criteria}</p>}{goal.ready_to_complete ? <em>所有必要子项已达成，可以确认完成</em> : goal.status === 'active' && <em>{goal.completion_summary.included_children ? `${goal.completion_summary.achieved_children}/${goal.completion_summary.included_children} 个必要子项已达成` : '还没有分目标或学习计划，添加一项开始推进'}</em>}</div>
      <div className="goal-actions">{goal.status === 'active' ? <>
        <button className="goal-plan-action" onClick={() => onAddPlan(goal)}><Plus size={14} />添加计划</button><button onClick={() => onAdd(goal)}>新增分目标</button>
        <details className="goal-menu"><summary>更多</summary><div><button onClick={() => onEdit(goal)}>编辑</button><button onClick={() => onComplete(goal)}>确认完成</button><button onClick={() => onAbandon(goal)}>放弃</button></div></details>
      </> : <><button onClick={() => onReopen(goal)}>恢复</button>{goal.status === 'abandoned' && goal.parent_goal_id && <button onClick={() => onTogglePolicy(goal)}>{goal.excluded_from_parent_completion ? '改为阻塞上层' : '改为排除'}</button>}</>}</div>
    </div>
    {expanded && <div className="goal-children">
      {goal.plans.length > 0 && <div className="goal-plans">{goal.plans.map(plan => <button key={plan.id} onClick={() => onOpenPlan(plan.id)}><Route size={15} /><span>{plan.title}<small>{plan.objective}</small></span><em>{plan.status === 'active' ? plan.active_version_id ? `${plan.done_tasks}/${plan.total_tasks} 任务` : '草稿待安排' : plan.status === 'achieved' ? '已达成' : '已放弃'}</em><ChevronRight size={15} /></button>)}</div>}
      {goal.children.map(child => <GoalBranch key={child.id} goal={child} depth={depth + 1} onEdit={onEdit} onAdd={onAdd} onAddPlan={onAddPlan} onComplete={onComplete} onAbandon={onAbandon} onReopen={onReopen} onTogglePolicy={onTogglePolicy} onOpenPlan={onOpenPlan} />)}
    </div>}
  </article>
}

function PlansPage({ token, onOpen }: { token: string; onOpen: (id: string) => void }) {
  const plans = useLoad(() => api<PlanSummary[]>(token, '/plans'), [token], ['plans'])
  const goals = useLoad(() => api<GoalNode[]>(token, '/goals/tree'), [token], ['goals'])
  const [showForm, setShowForm] = useState(false)
  const [goalID, setGoalID] = useState('')
  const flat = flattenGoals(goals.data ?? [])
  return <PageFrame eyebrow="学习计划" title="为目标安排一条可执行的路线" description="多个计划可以同时推进。每份计划先保存草稿，安排好任务后再启用。" action={<button className="button button-primary" onClick={() => setShowForm(true)} disabled={!goals.data}><Plus size={16} />新建计划</button>}>
    <div className="plans-filter-row"><label>所属目标 <select aria-label="按所属目标筛选计划" value={goalID} onChange={e => setGoalID(e.target.value)}><option value="">全部目标</option>{flat.map(({ goal, depth }) => <option key={goal.id} value={goal.id}>{'　'.repeat(depth)}{goal.title}</option>)}</select></label><span>{plans.data?.filter(plan => !goalID || plan.goal_id === goalID).length ?? 0} 份计划</span></div>
    {!plans.data ? <LoadingBlock error={plans.error} /> : <div className="plan-card-grid">{plans.data.filter(plan => !goalID || plan.goal_id === goalID).map(plan => <article className="plan-card" key={plan.id}><div className="plan-card-head"><span className="plan-type">{plan.current_mode === 'sequence' ? <BookOpen size={13} /> : <CalendarDays size={13} />}{plan.current_mode === 'sequence' ? '按课时' : plan.current_mode === 'calendar' ? '按日期' : '草稿待设置'}</span><SourceBadge source={plan.source} /><span className={plan.status === 'achieved' ? 'status-badge achieved' : plan.status === 'abandoned' ? 'status-badge abandoned' : plan.active_version_id ? 'status-badge active' : 'status-badge draft'}>{plan.status === 'achieved' ? '已达成' : plan.status === 'abandoned' ? '已放弃' : plan.active_version_id ? `V${plan.active_version_no} 进行中` : '草稿'}</span></div><h2>{plan.title}</h2><p className="plan-description"><strong>目标：</strong>{plan.objective}</p><p className="plan-parent">所属目标：{flat.find(item => item.goal.id === plan.goal_id)?.goal.title ?? '读取中'}</p><div className="plan-progress-copy"><span>当前版本任务</span><strong>{plan.done_tasks} / {plan.total_tasks}</strong></div><div className="progress-track"><span style={{ width: `${plan.total_tasks ? plan.done_tasks / plan.total_tasks * 100 : 0}%` }} /></div>{plan.draft_version_id && <div className="plan-next"><span>下一步</span><strong>继续安排草稿中的阶段和任务</strong></div>}<button className="button plan-open-button" onClick={() => onOpen(plan.id)}>{plan.active_version_id ? '查看计划' : '继续编排草稿'} <ChevronRight size={15} /></button></article>)}</div>}
    {plans.data && !plans.data.some(plan => !goalID || plan.goal_id === goalID) && <div className="empty-plans"><Route size={28} /><strong>{goalID ? '这个目标下还没有计划' : '还没有学习计划'}</strong><span>写清楚计划目标，选择推进方式，再添加具体任务。</span><button className="button" disabled={!goals.data} onClick={() => setShowForm(true)}>创建第一份计划</button></div>}
    {goals.error && <p role="alert" className="import-error">目标加载失败：{goals.error}<button className="button" onClick={() => void goals.reload()}>重试</button></p>}
    {showForm && <PlanCreator token={token} goals={goals.data ?? []} initialGoalID={goalID} onClose={() => setShowForm(false)} onCreated={onOpen} onGoalsChanged={() => { void goals.reload() }} />}
  </PageFrame>
}

function PlanDetailPage({ token, planID, onOpenTask }: { token: string; planID: string; onOpenTask: (task: Task, reload: () => void) => void }) {
  const { data, error, reload } = useLoad(() => api<PlanDetail>(token, `/plans/${planID}`), [token, planID], ['plan-detail'])
  const [selectedID, setSelectedID] = useState(() => new URLSearchParams(window.location.search).get('version_id') ?? '')
  const [versionEditor, setVersionEditor] = useState<'create' | 'edit' | null>(null)
  const [notice, setNotice] = useState('')
  const [showCanceled, setShowCanceled] = useState(false)
  useEffect(() => { setShowCanceled(false) }, [planID])
  useEffect(() => { if (data) {
    const visible = data.versions.filter(version => showCanceled || version.status !== 'canceled')
    const requested = new URLSearchParams(window.location.search).get('version_id')
    setSelectedID(current => {
      const previous = data.versions.find(version => version.id === current)
      if (previous?.status === 'canceled' && !showCanceled) return visible.find(version => version.status === 'active')?.id ?? visible[0]?.id ?? ''
      if (visible.some(version => version.id === current)) return current
      return (visible.find(version => version.id === requested) ?? visible.find(version => version.status === 'draft') ?? visible.find(version => version.status === 'active') ?? visible[0])?.id ?? ''
    })
  } }, [data, showCanceled])
  if (!data) return <PageFrame eyebrow="计划详情" title="读取计划" description="正在加载版本和任务树。"><LoadingBlock error={error} /></PageFrame>
  const visibleVersions = data.versions.filter(version => showCanceled || version.status !== 'canceled')
  const selected = visibleVersions.find(version => version.id === selectedID) ?? visibleVersions.find(version => version.status === 'active') ?? visibleVersions[0] ?? data.versions[0]
  const run = async (job: () => Promise<unknown>) => { setNotice(''); try { await job(); await reload() } catch (reason) { setNotice(messageOf(reason)) } }
  const activate = () => run(() => api(token, `/plan-versions/${selected.id}/activate`, { method: 'POST' }))
  const cancelDraft = () => run(async () => {
    await api(token, `/plan-versions/${selected.id}/cancel`, { method: 'POST' })
    setShowCanceled(false)
    setSelectedID(data.versions.find(version => version.status === 'active')?.id ?? '')
  })
  const createVersion = async (creationMode: 'blank' | 'copy') => {
    if (creationMode === 'blank') { setVersionEditor('create'); return }
    await run(async () => {
      const version = await api<PlanVersion>(token, `/plans/${data.plan.id}/versions`, { method: 'POST', body: JSON.stringify({ creation_mode: 'copy', source_version_id: selected.id }) })
      setSelectedID(version.id)
    })
  }
  const complete = async () => {
    try { await api(token, `/plans/${data.plan.id}/complete`, { method: 'POST' }) }
    catch (reason) {
      if (!completionWarning(reason, 'plan')) { setNotice(messageOf(reason)); return }
      await api(token, `/plans/${data.plan.id}/complete`, { method: 'POST', body: JSON.stringify({ acknowledge_incomplete: true }) })
    }
    await reload()
  }
  const abandon = async () => { const effect = chooseParentEffect(data.plan.title); if (effect) await run(() => api(token, `/plans/${data.plan.id}/abandon`, { method: 'POST', body: JSON.stringify({ parent_effect: effect }) })) }
  const reopen = () => run(() => api(token, `/plans/${data.plan.id}/reopen`, { method: 'POST' }))
  const togglePolicy = () => run(() => api(token, `/plans/${data.plan.id}/completion-policy`, { method: 'PATCH', body: JSON.stringify({ parent_effect: data.plan.excluded_from_goal_completion ? 'block' : 'exclude' }) }))
  const hasDraft = data.versions.some((version) => version.status === 'draft')
  const canceledCount = data.versions.filter(version => version.status === 'canceled').length
  if (!visibleVersions.length) return <PageFrame eyebrow="计划详情" title={data.plan.title} description={data.plan.description}>
    {versionEditor && <VersionEditor token={token} planID={data.plan.id} creating onClose={() => setVersionEditor(null)} onSaved={id => { setVersionEditor(null); setSelectedID(id); void reload() }} />}
    <p className="muted-copy">没有当前版本或可用草稿。已取消的草稿不会参与学习执行。</p>
    {notice && <p className="drawer-error">{notice}</p>}
    <div className="topbar-actions">{canceledCount > 0 && <button className="button" onClick={() => setShowCanceled(true)}>查看已取消草稿（{canceledCount}）</button>}{data.plan.status === 'active' && <button className="button" onClick={() => void createVersion('blank')}>新建草稿</button>}</div>
  </PageFrame>
  return <PageFrame eyebrow={selected.mode === 'calendar' ? '按日期版本' : '按课时版本'} title={data.plan.title} description={data.plan.description || '每个版本是一套独立方案，切换不会合并进度。'} action={<div className="topbar-actions">{data.plan.status === 'active' ? <><button className="button" onClick={complete}>确认达成</button><button className="button" onClick={abandon}>放弃计划</button></> : <><button className="button button-primary" onClick={reopen}>恢复计划</button>{data.plan.status === 'abandoned' && <button className="button" onClick={togglePolicy}>{data.plan.excluded_from_goal_completion ? '改为阻塞上层' : '改为从上层排除'}</button>}</>}</div>}>
    <section className="summary-strip plan-summary-strip"><div><span>计划目标</span><strong>{data.plan.objective}</strong></div><div><span>完成标准</span><strong>{data.plan.success_criteria || '由你最终判断'}</strong></div><div><span>目标日期</span><strong>{data.plan.target_date || '未设置'}</strong></div><div><span>计划状态</span><strong>{data.plan.status === 'active' ? '进行中' : data.plan.status === 'achieved' ? '已达成' : '已放弃'}</strong></div></section>
    {notice && <p className="drawer-error">{notice}</p>}
    <div className="version-tabs">{visibleVersions.map((version) => <button key={version.id} className={version.id === selected.id ? 'is-current' : ''} onClick={() => setSelectedID(version.id)}>V{version.version_no}<span>{version.status === 'draft' ? '草稿' : version.status === 'active' ? '当前版本' : version.status === 'canceled' ? '已取消' : '历史只读'}</span></button>)}</div>
    {canceledCount > 0 && <button className="button" onClick={() => setShowCanceled(value => !value)}>{showCanceled ? '收起已取消草稿' : `查看已取消草稿（${canceledCount}）`}</button>}
    {selected.status === 'canceled' && <p className="muted-copy">此草稿已取消，仅保留记录，不会参与学习执行，也不能激活。</p>}
    {data.plan.status === 'active' && <div className="topbar-actions">{!hasDraft && <><button className="button" onClick={() => void createVersion('blank')}><Plus size={15} />空白版本</button>{selected.status !== 'canceled' && <button className="button" onClick={() => void createVersion('copy')}>以 V{selected.version_no} 为模板</button>}</>}{selected.status === 'draft' && <><button className="button" onClick={() => setVersionEditor('edit')}>编辑版本设置</button><button className="button" onClick={cancelDraft}>取消草稿</button><button className="button button-primary" onClick={activate}>激活 V{selected.version_no}</button></>}{selected.status === 'superseded' && <button className="button button-primary" onClick={activate}>切换到 V{selected.version_no}</button>}</div>}
    <section className="detail-layout"><div className="detail-main"><PlanOutline key={selected.id} token={token} plan={data.plan} version={selected} reload={reload} onOpenTask={task => onOpenTask(task, reload)} /></div><aside className="detail-aside"><section><h3>版本信息</h3><dl><div><dt>版本</dt><dd>V{selected.version_no}</dd></div><div><dt>状态</dt><dd>{selected.status === 'draft' ? '草稿' : selected.status === 'active' ? '当前版本' : selected.status === 'canceled' ? '已取消' : '历史只读'}</dd></div><div><dt>推进方式</dt><dd>{selected.mode === 'calendar' ? '按日期' : '按课时'}</dd></div><div><dt>每周投入</dt><dd>{minutes(selected.weekly_capacity_minutes)}</dd></div><div><dt>开始日期</dt><dd>{selected.start_date || '未设置'}</dd></div><div><dt>结束日期</dt><dd>{selected.end_date || '未设置'}</dd></div><div><dt>模板来源</dt><dd>{selected.source_version_id ? `版本 ${data.versions.find(item => item.id === selected.source_version_id)?.version_no ?? '历史'}` : '空白创建'}</dd></div></dl></section>{selected.status === 'superseded' && <p className="muted-copy">历史版本只读；切换为当前版本后才能继续执行。</p>}</aside></section>
    {versionEditor && <VersionEditor token={token} planID={data.plan.id} version={selected} creating={versionEditor === 'create'} onClose={() => setVersionEditor(null)} onSaved={id => { setVersionEditor(null); setSelectedID(id); void reload() }} />}

  </PageFrame>
}

function TasksPage({ token, onOpen }: { token: string; onOpen: (task: Task, reload: () => void) => void }) {
  const { data, error, reload } = useLoad(() => api<Task[]>(token, '/tasks'), [token], ['tasks'])
  const [query, setQuery] = useState('')
  const [status, setStatus] = useState('')
  const visible = (data ?? []).filter((task) => task.title.toLowerCase().includes(query.toLowerCase()) && (!status || task.status === status))
  return <PageFrame eyebrow="任务中心" title="全部任务" description="统一查询所有生效计划中的任务。"><section className="task-toolbar"><label className="search-box"><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索任务" /></label><select value={status} onChange={(event) => setStatus(event.target.value)}><option value="">全部状态</option><option value="todo">待完成</option><option value="in_progress">进行中</option><option value="done">已完成</option></select><span /></section>{!data ? <LoadingBlock error={error} /> : <section className="task-table-panel">{visible.map((task) => <TaskRow key={task.id} task={task} onOpen={(value) => onOpen(value, reload)} onComplete={async (value) => { await api(token, `/tasks/${value.id}/${value.status === 'done' ? 'reopen' : 'complete'}`, { method: 'POST' }); await reload() }} />)}{!visible.length && <div className="empty-row">没有符合条件的任务</div>}</section>}</PageFrame>
}

function ReviewPage({ token }: { token: string }) {
  const { data, error } = useLoad(() => api<WeeklyReview>(token, '/reviews/weekly'), [token], ['reviews'])
  if (!data) return <PageFrame eyebrow="学习复盘" title="本周执行情况" description="只展示真实任务和计时记录。"><LoadingBlock error={error} /></PageFrame>
  const max = Math.max(1, ...data.daily.map((item) => item.duration_seconds))
  return <PageFrame eyebrow={`${data.week_start} — ${data.week_end}`} title="本周学习复盘" description="比较计划投入与实际执行，不直接推断能力是否掌握。">
    <section className="review-metrics"><div><span>完成任务</span><strong>{data.completed_tasks}</strong><em>本周完成</em></div><div><span>实际投入</span><strong>{hours(data.actual_duration_seconds)}</strong><em>计划 {minutes(data.planned_minutes)}</em></div><div><span>按期完成率</span><strong>{Math.round(data.on_time_rate * 100)}%</strong><em>{data.on_time_completed}/{data.scheduled_tasks} 项</em></div><div><span>当前逾期</span><strong>{data.current_overdue}</strong><em>{data.reschedule_count} 次改期</em></div></section>
    <div className="review-grid"><section className="review-card weekly-chart"><div className="review-card-head"><div><h2>每日投入</h2><p>按计时开始日期统计</p></div><strong>{hours(data.actual_duration_seconds)}</strong></div><div className="bar-chart">{data.daily.map((item) => <div className="bar-column" key={item.date}><span className="bar-value">{hours(item.duration_seconds)}</span><i style={{ height: `${item.duration_seconds / max * 100}%` }} /><em>{item.date.slice(5)}</em></div>)}</div></section><section className="review-card"><div className="review-card-head"><div><h2>各计划投入</h2><p>计划预算与实际计时</p></div></div><div className="distribution-list">{data.plans.map((plan) => <div key={plan.plan_id}><span>{plan.plan_title}</span><strong>{minutes(plan.planned_minutes)} / {hours(plan.actual_duration_seconds)}</strong></div>)}</div></section></div>
    <section className="review-card plan-review"><div className="review-card-head"><div><h2>各计划执行情况</h2><p>完成数、实际投入和改期记录</p></div></div><div className="plan-review-head"><span>计划</span><span>完成任务</span><span>计划 / 实际</span><span>改期</span><span>状态</span></div>{data.plans.map((plan) => <div className="plan-review-row" key={plan.plan_id}><strong>{plan.plan_title}</strong><span>{plan.completed_tasks}</span><span>{minutes(plan.planned_minutes)} / {hours(plan.actual_duration_seconds)}</span><span>{plan.reschedule_count} 次</span><span>{plan.actual_duration_seconds >= plan.planned_minutes * 60 ? '达到投入' : '继续推进'}</span></div>)}</section>
  </PageFrame>
}

function SettingsPage({ token, account, onLogout }: { token: string; account: Account; onLogout: () => void }) {
  const { data, error, reload } = useLoad(() => api<Preferences>(token, '/preferences'), [token], ['preferences'])
  const save = async (changes: Partial<Preferences>) => { await api(token, '/preferences', { method: 'PATCH', body: JSON.stringify(changes) }); await reload() }
  return <PageFrame eyebrow="个人空间" title="设置" description="管理计划偏好和当前账号。">
    <section className="settings-card account-card"><div className="settings-title"><span className="settings-icon"><ShieldCheck size={18} /></span><div><h2>共享认证账号</h2><p>StudyFlow 使用 shared-auth 提供的 UUID 身份。</p></div></div><div className="account-profile"><span className="large-avatar">{account.identities[0]?.value.slice(0, 1).toUpperCase() ?? 'U'}</span><div><strong>{account.identities[0]?.value ?? account.user.id}</strong><span>邮箱已验证 · {account.user.id}</span></div><button className="button" onClick={onLogout}><LogOut size={15} />退出登录</button></div></section>
    {!data ? <LoadingBlock error={error} /> : <section className="settings-card"><div className="settings-title"><span className="settings-icon"><Settings size={18} /></span><div><h2>计划偏好</h2><p>用于首页日期和周度复盘。</p></div></div><div className="setting-row"><div><strong>每周开始日</strong><span>决定周报统计范围</span></div><select value={data.week_start} onChange={(event) => save({ week_start: event.target.value as Preferences['week_start'] })}><option value="monday">星期一</option><option value="sunday">星期日</option></select></div><div className="setting-row"><div><strong>默认计划方式</strong><span>新建计划时的默认选择</span></div><select value={data.default_plan_mode} onChange={(event) => save({ default_plan_mode: event.target.value as PlanMode })}><option value="calendar">按日期</option><option value="sequence">按课时</option></select></div><div className="setting-row"><div><strong>首页显示已完成任务</strong><span>当天完成后继续保留</span></div><button className={data.show_completed_today ? 'switch is-on' : 'switch'} onClick={() => save({ show_completed_today: !data.show_completed_today })}><span /></button></div></section>}
  </PageFrame>
}

const sampleDraft = `{
  "title": "示例：三周复习计划",
  "description": "由 JobPilot 生成、用户确认后导入。",
  "objective": "完成三周复习并通过一轮自测",
  "success_criteria": "全部核心任务完成并完成复盘",
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
  const goals = useLoad(() => api<GoalNode[]>(token, '/goals/tree'), [token], ['goals'])
  const history = useLoad(() => api<PlanImportView[]>(token, '/plan-imports'), [token], ['imports'])
  const [goalID, setGoalID] = useState('')
  const [draft, setDraft] = useState('')
  const [key, setKey] = useState('')
  const [result, setResult] = useState<PlanImportResult | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const all = flattenGoals(goals.data ?? []).filter(({ goal }) => goal.status === 'active')
  useEffect(() => { if (!goalID && all[0]) setGoalID(all[0].goal.id) }, [goalID, all])

  // 改草稿或换目标都作废幂等键：同一份输入再点一次确认是重放，输入变了才算新的一次导入。
  const editDraft = (value: string) => { setDraft(value); setKey(''); setResult(null); setError('') }
  const changeGoal = (value: string) => { setGoalID(value); setKey(''); setResult(null); setError('') }

  let preview: { title: string; objective: string; criteria: string; mode: string; milestones: number; tasks: number } | null = null
  let parseError = ''
  if (draft.trim()) {
    try {
      const value = JSON.parse(draft) as ImportDraft
      const milestones = Array.isArray(value.milestones) ? value.milestones : []
      const loose = Array.isArray(value.tasks) ? value.tasks : []
      if (!(value.title ?? '').trim() || !(value.objective ?? '').trim()) throw new Error('missing_required_fields')
      preview = {
        title: (value.title ?? '').trim() || '（未命名）',
        objective: (value.objective ?? '').trim() || '（缺少计划目标）',
        criteria: (value.success_criteria ?? '').trim() || '未设置，由用户判断',
        mode: value.mode === 'sequence' ? '按课时' : '按日期',
        milestones: milestones.length,
        tasks: milestones.reduce((sum, item) => sum + (Array.isArray(item?.tasks) ? item.tasks.length : 0), 0) + loose.length,
      }
    } catch (reason) { parseError = reason instanceof Error && reason.message === 'missing_required_fields' ? '计划名称和计划目标都必须填写。' : '草稿不是合法的 JSON。' }
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
          <textarea value={draft} onChange={(event) => editDraft(event.target.value)} rows={16} spellCheck={false} placeholder='{"title": "…", "objective": "…", "mode": "calendar", "milestones": []}' />
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
            <div><dt>计划目标</dt><dd>{preview.objective}</dd></div>
            <div><dt>完成标准</dt><dd>{preview.criteria}</dd></div>
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

export default function App() {
  const authGeneration = useRef(0)
  const signingOut = useRef(false)
  const accountID = useRef<string | null>(null)
  const [token, setToken] = useState('')
  const [account, setAccount] = useState<Account | null>(null)
  const [booting, setBooting] = useState(true)
  const [loggingOut, setLoggingOut] = useState(false)
  const [page, setPage] = useState<Page>(() => new URLSearchParams(window.location.search).has('plan_id') ? 'plan-detail' : new URLSearchParams(window.location.search).has('goal_id') ? 'goals' : 'today')
  const [planID, setPlanID] = useState(() => new URLSearchParams(window.location.search).get('plan_id') ?? '')
  const [selectedTask, setSelectedTask] = useState<Task | null>(null)
  const [drawerReload, setDrawerReload] = useState<(() => void) | null>(null)
  const [running, setRunning] = useState<Session | null>(null)
  const [notice, setNotice] = useState('')

  const accept = useCallback((issue: { access_token: string; account: Account }) => {
    if (accountID.current && accountID.current !== issue.account.user.id) { setSelectedTask(null); setRunning(null); setDrawerReload(null); setPlanID(''); setPage('today'); setNotice('') }
    accountID.current = issue.account.user.id
    setToken(issue.access_token); setAccount(issue.account)
  }, [])
  useEffect(() => { refreshAccess().then(accept).catch(() => undefined).finally(() => setBooting(false)) }, [accept])
  const openTask = (task: Task, reload: () => void) => { setSelectedTask(task); setDrawerReload(() => reload); void api<Dashboard>(token, '/dashboard/today').then((value) => setRunning(value.running_session?.session ?? null)) }
  const changed = async () => { setSelectedTask(null); setRunning(null); drawerReload?.(); setNotice('操作已保存。') }
  const openPlan = (id: string) => { setPlanID(id); setPage('plan-detail') }
  const signOut = async () => { signingOut.current = true; authGeneration.current++; setLoggingOut(true); try { await logout() } catch (reason) { setNotice(messageOf(reason)) } finally { if (account) clearSyncCursor(account.user.id); setSelectedTask(null); setRunning(null); setDrawerReload(null); setPlanID(''); setPage('today'); setToken(''); setAccount(null); signingOut.current = false; setLoggingOut(false) } }
  const renew = useCallback(async () => {
    if (signingOut.current) return
    const generation = authGeneration.current
    try { const issue = await refreshAccess(); if (generation === authGeneration.current && !signingOut.current) accept(issue) }
    catch (reason) { if (generation !== authGeneration.current || signingOut.current) return; if (reason instanceof APIError && reason.status === 401) { setToken(''); setAccount(null) } else { throw reason } }
  }, [accept])
  useEffect(() => { if (!token) return; const timer = window.setInterval(() => { void renew().catch(() => undefined) }, 12 * 60 * 1000); return () => window.clearInterval(timer) }, [token, renew])
  if (booting) return <main className="auth-shell"><div className="auth-card"><strong>正在恢复登录状态…</strong></div></main>
  if (loggingOut) return <main className="auth-shell"><div className="auth-card"><strong>正在退出登录…</strong></div></main>
  if (!token || !account) return <AuthPage onAuthenticated={accept} />
  const title = page === 'today' ? ['今天', new Date().toLocaleDateString('zh-CN', { month: 'long', day: 'numeric', weekday: 'long' })] : page === 'goals' ? ['目标', '递归拆解长期目标'] : page === 'plans' ? ['学习计划', '多个计划同步推进'] : page === 'plan-detail' ? ['计划详情', '版本、阶段与任务'] : page === 'tasks' ? ['全部任务', '查询所有生效计划'] : page === 'reviews' ? ['学习复盘', '本周执行情况'] : page === 'imports' ? ['计划导入', '确认后写入'] : ['设置', '账号与计划偏好']
  return <SyncProvider key={account.user.id} token={token} userID={account.user.id} onUnauthorized={renew}><div className="app-shell"><aside className="sidebar"><div className="brand"><span className="brand-mark">S</span><strong>StudyFlow</strong></div><nav className="navigation">{nav.map((item) => { const Icon = item.icon; return <button key={item.id} className={page === item.id || (page === 'plan-detail' && item.id === 'plans') ? 'nav-button is-current' : 'nav-button'} onClick={() => { setPage(item.id); setNotice('') }}><Icon size={18} /><span>{item.label}</span></button> })}</nav><div className="sidebar-footer"><span className="user-avatar">{account.identities[0]?.value.slice(0, 1).toUpperCase() ?? 'U'}</span><div><strong>{account.identities[0]?.value ?? '已登录用户'}</strong><span>个人空间</span></div><ChevronDown size={16} /></div></aside><main className="main-area"><SyncStatus /><header className="topbar"><div><span>{title[0]}</span><strong>{title[1]}</strong></div></header>{notice && <div className="notice"><span>{notice}</span><button onClick={() => setNotice('')}>×</button></div>}{page === 'today' && <TodayPage token={token} onOpen={openTask} />}{page === 'goals' && <GoalsPage token={token} onOpenPlan={openPlan} />}{page === 'plans' && <PlansPage token={token} onOpen={openPlan} />}{page === 'plan-detail' && planID && <PlanDetailPage token={token} planID={planID} onOpenTask={openTask} />}{page === 'tasks' && <TasksPage token={token} onOpen={openTask} />}{page === 'reviews' && <ReviewPage token={token} />}{page === 'settings' && <SettingsPage token={token} account={account} onLogout={signOut} />}{page === 'imports' && <ImportsPage token={token} onOpenPlan={openPlan} />}</main>{selectedTask && <TaskDrawer token={token} task={selectedTask} running={running} onClose={() => setSelectedTask(null)} onChanged={changed} />}</div></SyncProvider>
}
