import { FormEvent, ReactNode, useEffect, useId, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ArrowLeft, ArrowRight, BookOpen, CalendarDays, Check, Plus, X } from 'lucide-react'
import { api } from './api'
import type { GoalNode, PlanDetail, PlanMode, PlanVersion } from './types'

export function errorMessage(error: unknown) { return error instanceof Error ? error.message : '保存失败，请稍后重试。' }

export function EditorDialog({ title, description, onClose, children }: { title: string; description: string; onClose: () => void; children: ReactNode }) {
  const id = useId()
  const panel = useRef<HTMLDivElement>(null)
  const close = useRef(onClose)
  close.current = onClose
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    const overflow = document.body.style.overflow
    const siblings = Array.from(document.body.children).filter((node): node is HTMLElement => node instanceof HTMLElement && !node.contains(panel.current))
    const oldState = siblings.map(node => ({ node, inert: node.inert, hidden: node.getAttribute('aria-hidden') }))
    siblings.forEach(node => { node.inert = true; node.setAttribute('aria-hidden', 'true') })
    document.body.style.overflow = 'hidden'
    panel.current?.focus()
    return () => {
      oldState.forEach(({ node, inert, hidden }) => { node.inert = inert; if (hidden === null) node.removeAttribute('aria-hidden'); else node.setAttribute('aria-hidden', hidden) })
      document.body.style.overflow = overflow; previous?.focus()
    }
  }, [])
  return createPortal(<div className="editor-backdrop" onClick={event => { if (event.target === event.currentTarget) close.current() }}>
    <div ref={panel} className="editor-dialog" role="dialog" aria-modal="true" aria-labelledby={id} aria-describedby={`${id}-description`} tabIndex={-1}
      onKeyDown={event => {
        if (event.key === 'Escape') { event.stopPropagation(); close.current() }
        if (event.key !== 'Tab') return
        const fields = Array.from(panel.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), summary, [tabindex="0"]') ?? []).filter(el => !el.closest('details:not([open])') || el.tagName === 'SUMMARY')
        const first = fields[0], last = fields.at(-1)
        if (event.shiftKey && (document.activeElement === first || document.activeElement === panel.current)) { event.preventDefault(); last?.focus() }
        else if (!event.shiftKey && (document.activeElement === last || document.activeElement === panel.current)) { event.preventDefault(); first?.focus() }
      }}>
      <header className="editor-header"><div><h2 id={id}>{title}</h2><p id={`${id}-description`}>{description}</p></div><button type="button" className="editor-close" aria-label="关闭弹窗" onClick={onClose}><X size={20} /></button></header>
      {children}
    </div>
  </div>, document.body)
}

export function goalOptions(nodes: GoalNode[], prefix = '', available = true): Array<{ goal: GoalNode; path: string; available: boolean }> {
  return nodes.flatMap(goal => {
    const path = prefix ? `${prefix} / ${goal.title}` : goal.title
    const active = available && goal.status === 'active'
    return [{ goal, path, available: active }, ...goalOptions(goal.children, path, active)]
  })
}

export function GoalEditor({ token, goals, initial, parentID = '', onClose, onSaved }: {
  token: string; goals: GoalNode[]; initial?: GoalNode; parentID?: string; onClose: () => void; onSaved: (goal: GoalNode) => void
}) {
  const [snapshot, setSnapshot] = useState(initial)
  const [title, setTitle] = useState(initial?.title ?? '')
  const [description, setDescription] = useState(initial?.description ?? '')
  const [criteria, setCriteria] = useState(initial?.success_criteria ?? '')
  const [date, setDate] = useState(initial?.target_date ?? '')
  const [parent, setParent] = useState(initial?.parent_goal_id ?? parentID)
  const [busy, setBusy] = useState(false)
  const pending = useRef(false)
  const [error, setError] = useState('')
  const options = goalOptions(goals)
  const current = snapshot ? options.find(item => item.goal.id === snapshot.id)?.goal : undefined
  const excluded = new Set(snapshot ? goalOptions([current ?? snapshot]).map(item => item.goal.id) : [])
  const stale = Boolean(snapshot && (!current || current.version !== snapshot.version || current.status !== 'active'))
  const dirty = title !== (snapshot?.title ?? '') || description !== (snapshot?.description ?? '') || criteria !== (snapshot?.success_criteria ?? '') || date !== (snapshot?.target_date ?? '') || parent !== (snapshot?.parent_goal_id ?? parentID)
  const close = () => { if (!pending.current && (!dirty || window.confirm('放弃尚未保存的目标内容？'))) onClose() }
  const reload = () => {
    if (!current || !window.confirm('重新加载会放弃尚未保存的修改，继续吗？')) return
    setSnapshot(current); setTitle(current.title); setDescription(current.description); setCriteria(current.success_criteria); setDate(current.target_date ?? ''); setParent(current.parent_goal_id ?? ''); setError('')
  }
  const validParent = !parent || options.some(item => item.goal.id === parent && item.available && !excluded.has(parent))
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (pending.current || stale || !validParent) return
    if (!title.trim()) { setError('请填写目标名称。'); return }
    pending.current = true; setBusy(true); setError('')
    try {
      const saved = await api<GoalNode>(token, snapshot ? `/goals/${snapshot.id}` : '/goals', { method: snapshot ? 'PATCH' : 'POST', body: JSON.stringify({
        title: title.trim(), description: description.trim(), success_criteria: criteria.trim(), target_date: date || undefined,
        parent_goal_id: parent || undefined, ...(snapshot ? { version: snapshot.version, clear_parent: !parent, clear_target_date: !date } : {}),
      }) })
      onSaved(saved)
    } catch (reason) { setError(errorMessage(reason)) } finally { pending.current = false; setBusy(false) }
  }
  return <EditorDialog title={snapshot ? '编辑目标' : parentID ? '新建分目标' : '新建目标'} description="写清楚想达成的结果，再用分目标或学习计划把它拆开。" onClose={close}>
    <form onSubmit={submit} className="editor-form">
      <div className="editor-body">
        {stale && <div role="alert" className="sync-edit-warning">目标已在其他客户端变更，输入已保留。{current?.status === 'active' && <button type="button" className="button" onClick={reload}>重新加载最新数据</button>}</div>}
        <label className="editor-field"><span>目标名称 <em>必填</em></span><input required maxLength={160} placeholder="例如：能独立开发并上线一个后端服务" value={title} onChange={e => setTitle(e.target.value)} /></label>
        <label className="editor-field"><span>所属目标</span><select value={parent} onChange={e => setParent(e.target.value)}><option value="">无，作为顶层目标</option>{options.filter(item => item.available && !excluded.has(item.goal.id)).map(item => <option key={item.goal.id} value={item.goal.id}>{item.path}</option>)}</select><small>选一个已有目标，就会创建它的分目标；层级可以之后调整。</small></label>
        {!validParent && <p role="alert" className="import-error">所选上级目标已不可用，请重新选择。</p>}
        <label className="editor-field"><span>完成标准 <em>选填</em></span><textarea rows={3} placeholder="做到什么程度，就算达成？" value={criteria} onChange={e => setCriteria(e.target.value)} /></label>
        <div className="editor-grid"><label className="editor-field"><span>目标日期 <em>选填</em></span><input type="date" value={date} onChange={e => setDate(e.target.value)} /></label><p className="editor-hint">日期只用于提醒，不会自动把任务排进日程。</p></div>
        <details className="editor-more" open={Boolean(snapshot?.description) || undefined}><summary>补充说明（选填）</summary><label className="editor-field"><span>目标说明</span><textarea rows={3} value={description} onChange={e => setDescription(e.target.value)} /></label></details>
        {error && <p className="import-error" role="alert">{error}</p>}
      </div>
      <footer className="editor-footer"><button type="button" className="button" disabled={busy} onClick={close}>取消</button><button className="button button-primary" disabled={busy || stale || !validParent}>{busy ? '保存中…' : snapshot ? '保存修改' : '创建目标'}</button></footer>
    </form>
  </EditorDialog>
}

export function ModeFields({ mode, onMode, capacity, onCapacity, start, onStart, end, onEnd, lockMode = false }: {
  mode: PlanMode; onMode: (mode: PlanMode) => void; capacity: string; onCapacity: (value: string) => void
  start: string; onStart: (value: string) => void; end: string; onEnd: (value: string) => void; lockMode?: boolean
}) {
  return <>
    <fieldset className="mode-fieldset"><legend>如何推进？</legend><div className="mode-choices">
      {([{ value: 'calendar', title: '按日期安排', copy: '任务安排到某一天，不规定几点开始。', icon: CalendarDays }, { value: 'sequence', title: '按课时推进', copy: '不设任务日期，完成一项再继续下一项。', icon: BookOpen }] as const).map(option => <label key={option.value} className={mode === option.value ? 'mode-choice is-selected' : 'mode-choice'}>
        <input type="radio" name="plan-mode" value={option.value} checked={mode === option.value} disabled={lockMode && option.value !== mode} onChange={() => onMode(option.value)} /><option.icon size={20} /><strong>{option.title}</strong><span>{option.copy}</span>{mode === option.value && <Check size={16} className="mode-selected" />}
      </label>)}
    </div></fieldset>
    {lockMode && <p className="editor-hint">此草稿已有按日期安排的任务。请先清除任务日期，才能改为按课时。</p>}
    <label className="editor-field"><span>每周预计投入（小时）</span><input type="number" required min="0" max="168" step="any" value={capacity} onChange={e => onCapacity(e.target.value)} /><small>用于复盘时对照实际投入，0 表示暂不设预算。</small></label>
    <div className="editor-grid"><label className="editor-field"><span>开始日期 <em>选填</em></span><input type="date" value={start} onChange={e => onStart(e.target.value)} /></label><label className="editor-field"><span>结束日期 <em>选填</em></span><input type="date" min={start || undefined} value={end} onChange={e => onEnd(e.target.value)} /></label></div>
    <p className="editor-hint">计划周期用于参考，任务需要单独安排。各计划版本可以采用不同的推进方式。</p>
  </>
}

export function PlanCreator({ token, goals, initialGoalID = '', onClose, onCreated, onGoalsChanged }: {
  token: string; goals: GoalNode[]; initialGoalID?: string; onClose: () => void; onCreated: (id: string) => void; onGoalsChanged: () => void
}) {
  const [step, setStep] = useState(1)
  const [goalID, setGoalID] = useState(initialGoalID)
  const [addedGoals, setAddedGoals] = useState<GoalNode[]>([])
  const [showGoal, setShowGoal] = useState(false)
  const [title, setTitle] = useState('')
  const [objective, setObjective] = useState('')
  const [criteria, setCriteria] = useState('')
  const [targetDate, setTargetDate] = useState('')
  const [description, setDescription] = useState('')
  const [mode, setMode] = useState<PlanMode>('calendar')
  const [capacity, setCapacity] = useState('10')
  const [start, setStart] = useState('')
  const [end, setEnd] = useState('')
  const [busy, setBusy] = useState(false)
  const pending = useRef(false)
  const [error, setError] = useState('')
  const options = goalOptions(goals)
  const extra = addedGoals.filter(goal => !options.some(item => item.goal.id === goal.id)).map(goal => ({ goal, path: goal.title, available: true }))
  const available = [...options, ...extra].filter(item => item.available)
  const selectedGoal = available.find(item => item.goal.id === goalID)
  const dirty = Boolean(title || objective || criteria || targetDate || description || start || end || mode !== 'calendar' || capacity !== '10')
  const close = () => { if (!pending.current && (!dirty || window.confirm('放弃尚未保存的计划内容？'))) onClose() }
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (pending.current) return
    if (!selectedGoal || !title.trim() || !objective.trim()) { setError('请选择所属目标，并填写计划名称和计划目标。'); setStep(1); return }
    if (step === 1) { setError(''); setStep(2); return }
    if (!Number.isFinite(Number(capacity)) || Number(capacity) < 0 || Number(capacity) > 168 || (start && end && start > end)) { setError('请检查每周投入与计划起止日期。'); return }
    pending.current = true; setBusy(true); setError('')
    try {
      const detail = await api<PlanDetail>(token, `/goals/${goalID}/plans`, { method: 'POST', body: JSON.stringify({ title: title.trim(), objective: objective.trim(), success_criteria: criteria.trim(), description: description.trim(), target_date: targetDate || undefined, mode, weekly_capacity_minutes: Math.round(Number(capacity) * 60), start_date: start || undefined, end_date: end || undefined }) })
      onCreated(detail.plan.id)
    } catch (reason) { setError(errorMessage(reason)) } finally { pending.current = false; setBusy(false) }
  }
  return <>
    <EditorDialog title="新建学习计划" description="先确定想达成的结果，再选择适合自己的推进方式。" onClose={close}>
      <form onSubmit={submit} className="editor-form">
        <ol className="creation-steps" aria-label="创建步骤"><li className={step === 1 ? 'is-current' : 'is-finished'}><span>{step === 2 ? <Check size={14} /> : '1'}</span>确定计划目标</li><li className={step === 2 ? 'is-current' : ''}><span>2</span>选择推进方式</li></ol>
        <div className="editor-body" key={step}>
          {step === 1 ? <>
            <label className="editor-field"><span>所属目标 <em>必填</em></span><select required value={goalID} onChange={e => setGoalID(e.target.value)}><option value="">选择这份计划要服务的目标</option>{available.map(item => <option key={item.goal.id} value={item.goal.id}>{item.path}</option>)}</select></label>
            <div className="editor-hint-row"><span>{available.length ? '计划可以放在任意一级目标下。' : '还没有可用目标，先创建一个，计划内容会保留。'}</span><button type="button" className="text-action" onClick={() => setShowGoal(true)}><Plus size={14} />新建目标</button></div>
            <label className="editor-field"><span>计划名称 <em>必填</em></span><input required maxLength={160} placeholder="计划名称" value={title} onChange={e => setTitle(e.target.value)} /><small>例如：后端鉴权学习计划</small></label>
            <label className="editor-field"><span>计划目标 <em>必填</em></span><textarea required rows={3} placeholder="完成计划后要达成什么目标" value={objective} onChange={e => setObjective(e.target.value)} /><small>例如：能够独立实现并解释注册、登录与令牌刷新。</small></label>
            <details className="editor-more"><summary>完成标准、目标日期与说明（选填）</summary>
              <label className="editor-field"><span>完成标准</span><textarea rows={2} placeholder="完成标准（可选）" value={criteria} onChange={e => setCriteria(e.target.value)} /></label>
              <label className="editor-field"><span>计划目标日期</span><input type="date" value={targetDate} onChange={e => setTargetDate(e.target.value)} /></label>
              <label className="editor-field"><span>计划说明</span><textarea rows={2} value={description} onChange={e => setDescription(e.target.value)} /></label>
            </details>
          </> : <>
            <div className="creation-recap"><span>{selectedGoal?.path ?? '所属目标已不可用，请返回重选'}</span><strong>{title}</strong><p>{objective}</p></div>
            <ModeFields mode={mode} onMode={setMode} capacity={capacity} onCapacity={setCapacity} start={start} onStart={setStart} end={end} onEnd={setEnd} />
            <div className="creation-note">下一步安排阶段和任务。新计划先保存为 V1 草稿，启用后才会出现在今日安排中。</div>
          </>}
          {error && <p role="alert" className="import-error">{error}</p>}
        </div>
        <footer className="editor-footer"><button type="button" className="button" disabled={busy} onClick={step === 1 ? close : () => { setStep(1); setError('') }}>{step === 1 ? '取消' : <><ArrowLeft size={15} />上一步</>}</button><button className="button button-primary" disabled={busy}>{busy ? '创建中…' : step === 1 ? <>下一步<ArrowRight size={15} /></> : '创建并安排任务'}</button></footer>
      </form>
    </EditorDialog>
    {showGoal && <GoalEditor token={token} goals={goals} onClose={() => setShowGoal(false)} onSaved={goal => { setAddedGoals(current => [...current, goal]); setGoalID(goal.id); setShowGoal(false); onGoalsChanged() }} />}
  </>
}

export function VersionEditor({ token, planID, version, creating, onClose, onSaved }: { token: string; planID: string; version?: PlanVersion; creating: boolean; onClose: () => void; onSaved: (id: string) => void }) {
  const [mode, setMode] = useState<PlanMode>(version?.mode ?? 'calendar')
  const [capacity, setCapacity] = useState(String((version?.weekly_capacity_minutes ?? 600) / 60))
  const [start, setStart] = useState(version?.start_date ?? '')
  const [end, setEnd] = useState(version?.end_date ?? '')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const pending = useRef(false)
  const scheduled = !creating && Boolean(version && [...version.unassigned_tasks, ...version.milestones.flatMap(item => item.tasks)].some(task => task.scheduled_date))
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (pending.current) return
    if (start && end && start > end) { setError('结束日期不能早于开始日期。'); return }
    pending.current = true; setBusy(true); setError('')
    try {
      const saved = await api<PlanVersion>(token, creating ? `/plans/${planID}/versions` : `/plan-versions/${version!.id}`, { method: creating ? 'POST' : 'PATCH', body: JSON.stringify({ ...(creating ? { creation_mode: 'blank' } : { clear_start_date: !start, clear_end_date: !end }), mode, weekly_capacity_minutes: Math.round(Number(capacity) * 60), start_date: start || undefined, end_date: end || undefined }) })
      onSaved(saved.id)
    } catch (reason) { setError(errorMessage(reason)) } finally { pending.current = false; setBusy(false) }
  }
  return <EditorDialog title={creating ? '创建空白计划版本' : `编辑 V${version?.version_no} 设置`} description="这里只设置这份计划版本，其他版本保持原样。" onClose={() => { if (!pending.current) onClose() }}><form className="editor-form" onSubmit={submit}><div className="editor-body"><ModeFields mode={mode} onMode={setMode} capacity={capacity} onCapacity={setCapacity} start={start} onStart={setStart} end={end} onEnd={setEnd} lockMode={scheduled} />{error && <p role="alert" className="import-error">{error}</p>}</div><footer className="editor-footer"><button type="button" className="button" disabled={busy} onClick={onClose}>取消</button><button className="button button-primary" disabled={busy}>{busy ? '保存中…' : creating ? '创建空白草稿' : '保存设置'}</button></footer></form></EditorDialog>
}
