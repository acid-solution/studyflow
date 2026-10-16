import { FormEvent, useRef, useState } from 'react'
import { BookOpen, Check, ChevronRight, Circle, Plus, Route } from 'lucide-react'
import { api } from './api'
import { EditorDialog, errorMessage } from './CreationDialogs'
import type { PlanDetail, PlanVersion, Task } from './types'

export default function PlanOutline({ token, plan, version, reload, onOpenTask }: {
  token: string; plan: PlanDetail['plan']; version: PlanVersion; reload: () => Promise<unknown>; onOpenTask: (task: Task) => void
}) {
  const [composer, setComposer] = useState<{ kind: 'task' | 'milestone'; milestoneID?: string } | null>(null)
  const editable = plan.status === 'active' && (version.status === 'draft' || version.status === 'active')
  const tasks = [...version.unassigned_tasks, ...version.milestones.flatMap(item => item.tasks)]
  const open = (task: Task) => onOpenTask({ ...task, plan_id: plan.id, plan_title: plan.title, plan_mode: version.mode, plan_status: plan.status, version_status: version.status })
  const taskRows = (items: Task[]) => <div className="stage-tasks">{items.map(task => <button key={task.id} className={task.status === 'done' ? 'stage-task is-done' : 'stage-task'} onClick={() => open(task)}><span>{task.status === 'done' ? <Check size={14} /> : <Circle size={14} />}</span><strong>{task.title}</strong><em>{task.status === 'done' ? '已完成' : task.status === 'canceled' ? '已取消' : task.scheduled_date ?? (version.mode === 'sequence' ? '按顺序推进' : '未安排日期')}</em><ChevronRight size={15} /></button>)}</div>
  const addTask = (milestoneID?: string) => <button className="outline-add-task" onClick={() => setComposer({ kind: 'task', milestoneID })}><Plus size={15} />添加任务</button>
  return <>
    {plan.status === 'active' && version.status === 'draft' && <section className="draft-guide">
      <div className="draft-guide-icon"><Route size={23} /></div><div><h2>先把路线排好，再开始执行</h2><p>可以先添加任务，也可以按阶段拆分。草稿中的内容会逐项保存，启用后进入今日安排。</p><div className="draft-checklist"><span className="is-done"><Check size={14} />目标已确定</span><span className={tasks.length ? 'is-done' : ''}>{tasks.length ? <Check size={14} /> : '2'} 添加阶段与任务</span><span>3 确认后启用版本</span></div></div>
    </section>}
    <div className="outline-toolbar"><div><h2>阶段与任务</h2><span>{version.milestones.length} 个阶段 · {tasks.length} 项任务</span></div>{editable && <div className="outline-actions">{version.status === 'draft' && <button className="button" onClick={() => setComposer({ kind: 'milestone' })}><Plus size={15} />添加阶段</button>}<button className="button button-primary" onClick={() => setComposer({ kind: 'task' })}><Plus size={15} />添加任务</button></div>}</div>
    {!tasks.length && !version.milestones.length ? <section className="outline-empty"><BookOpen size={29} /><h3>从一件能完成的小事开始</h3><p>例如“阅读登录接口代码，并画出请求处理流程”。任务写得具体，执行起来更容易。</p>{editable && <button className="button" onClick={() => setComposer({ kind: 'task' })}>添加第一项任务</button>}</section> : <div className="stage-list">
      {version.milestones.map((milestone, index) => <section className="stage-card" key={milestone.id}><div className="stage-head"><div><span>{String(index + 1).padStart(2, '0')}</span><strong>{milestone.title}</strong></div><span>{milestone.tasks.filter(task => task.status === 'done').length} / {milestone.tasks.length} 已完成</span></div>{milestone.outcome && <p className="stage-outcome">{milestone.outcome}</p>}{taskRows(milestone.tasks)}{!milestone.tasks.length && <p className="outline-stage-empty">这个阶段还没有任务。</p>}{editable && addTask(milestone.id)}</section>)}
      {version.unassigned_tasks.length > 0 && <section className="stage-card"><div className="stage-head"><strong>未分阶段</strong><span>{version.unassigned_tasks.length} 项任务</span></div>{taskRows(version.unassigned_tasks)}{editable && addTask()}</section>}
    </div>}
    {composer && <OutlineComposer token={token} version={version} kind={composer.kind} initialMilestoneID={composer.milestoneID} editable={editable && (composer.kind === 'task' || version.status === 'draft')} onClose={() => setComposer(null)} onSaved={reload} />}
  </>
}

function OutlineComposer({ token, version, kind, initialMilestoneID = '', editable, onClose, onSaved }: {
  token: string; version: PlanVersion; kind: 'task' | 'milestone'; initialMilestoneID?: string; editable: boolean; onClose: () => void; onSaved: () => Promise<unknown>
}) {
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [milestoneID, setMilestoneID] = useState(initialMilestoneID)
  const [date, setDate] = useState('')
  const [estimate, setEstimate] = useState('30')
  const [keepAdding, setKeepAdding] = useState(false)
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const pending = useRef(false)
  const titleInput = useRef<HTMLInputElement>(null)
  const modeAtOpen = useRef(version.mode).current
  const allowed = editable && version.mode === modeAtOpen && (!milestoneID || version.milestones.some(item => item.id === milestoneID))
  const dirty = Boolean(title || body || date || estimate !== '30' || milestoneID !== initialMilestoneID)
  const close = () => { if (!pending.current && (!dirty || window.confirm('放弃尚未保存的内容？'))) onClose() }
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!allowed || pending.current) return
    if (!title.trim()) { setError('请填写名称。'); return }
    pending.current = true; setBusy(true); setError(''); setNotice('')
    try {
      await api(token, `/plan-versions/${version.id}/${kind === 'task' ? 'tasks' : 'milestones'}`, { method: 'POST', body: JSON.stringify(kind === 'task' ? { title: title.trim(), description: body.trim(), milestone_id: milestoneID || undefined, scheduled_date: modeAtOpen === 'calendar' && date ? date : undefined, estimate_minutes: Number(estimate) } : { title: title.trim(), outcome: body.trim() }) })
      void onSaved()
      if (keepAdding) { setTitle(''); setBody(''); setNotice(`已保存“${title.trim()}”，可以继续添加。`); titleInput.current?.focus() }
      else onClose()
    } catch (reason) { setError(errorMessage(reason)) } finally { pending.current = false; setBusy(false) }
  }
  return <EditorDialog title={kind === 'task' ? '添加学习任务' : '添加学习阶段'} description={kind === 'task' ? '把行动写具体，每项任务都可以单独计时和记录。' : '阶段用来组织任务，不必为了分阶段而分阶段。'} onClose={close}><form className="editor-form" onSubmit={submit}><div className="editor-body">
    {!allowed && <p role="alert" className="sync-edit-warning">计划版本或阶段已发生变化，当前输入已保留。请关闭后核对最新内容再添加。</p>}
    <label className="editor-field"><span>{kind === 'task' ? '任务名称' : '阶段名称'} <em>必填</em></span><input ref={titleInput} required maxLength={kind === 'task' ? 255 : 160} value={title} placeholder={kind === 'task' ? '例如：实现登录接口的参数校验' : '例如：打通注册与登录'} onChange={e => setTitle(e.target.value)} /></label>
    {kind === 'task' && <>
      <label className="editor-field"><span>所属阶段</span><select value={milestoneID} onChange={e => setMilestoneID(e.target.value)}><option value="">暂不分阶段</option>{version.milestones.map(item => <option key={item.id} value={item.id}>{item.title}</option>)}</select></label>
      <div className="editor-grid">{modeAtOpen === 'calendar' && <label className="editor-field"><span>安排日期 <em>选填</em></span><input type="date" value={date} onChange={e => setDate(e.target.value)} /></label>}<label className="editor-field"><span>预计用时（分钟）</span><input type="number" required min="0" step="1" value={estimate} onChange={e => setEstimate(e.target.value)} /></label></div>
      <p className="editor-hint">{modeAtOpen === 'calendar' ? '可以暂不排日期，之后再安排。' : '任务会追加到所选阶段的末尾，按顺序推进。'}</p>
    </>}
    <label className="editor-field"><span>{kind === 'task' ? '任务说明' : '阶段预期成果'} <em>选填</em></span><textarea rows={3} value={body} placeholder={kind === 'task' ? '记录要做什么、参考资料或验收方式' : '完成这个阶段后，你希望得到什么结果？'} onChange={e => setBody(e.target.value)} /></label>
    <label className="editor-checkbox"><input type="checkbox" checked={keepAdding} onChange={e => setKeepAdding(e.target.checked)} />保存后继续添加</label>
    {notice && <p role="status" className="creation-note">{notice}</p>}{error && <p role="alert" className="import-error">{error}</p>}
  </div><footer className="editor-footer"><button className="button" type="button" disabled={busy} onClick={close}>取消</button><button className="button button-primary" disabled={busy || !allowed}>{busy ? '保存中…' : kind === 'task' ? '保存任务' : '保存阶段'}</button></footer></form></EditorDialog>
}
