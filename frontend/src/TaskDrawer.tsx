import { useEffect, useState } from 'react'
import { Clock3, Timer, X } from 'lucide-react'
import { api, APIError } from './api'
import { useLoad } from './data'
import type { Dashboard, Session, Task } from './types'

export default function TaskDrawer({ token, task, running, onClose, onChanged }: { token: string; task: Task; running: Session | null; onClose: () => void; onChanged: () => void }) {
  const detail = useLoad(() => api<Task>(token, `/tasks/${task.id}`), [token, task.id], ['tasks', 'plan-detail'])
  const history = useLoad(() => api<Session[]>(token, `/tasks/${task.id}/sessions`), [token, task.id], ['sessions'])
  const dashboard = useLoad(() => api<Dashboard>(token, '/dashboard/today'), [token], ['dashboard'])
  const [baseline, setBaseline] = useState(task)
  const [title, setTitle] = useState(task.title)
  const [description, setDescription] = useState(task.description)
  const [date, setDate] = useState(task.scheduled_date ?? '')
  const [estimate, setEstimate] = useState(task.estimate_minutes)
  const [sessionNote, setSessionNote] = useState('')
  const [noteSessionID, setNoteSessionID] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const dirty = title !== baseline.title || description !== baseline.description || date !== (baseline.scheduled_date ?? '') || estimate !== baseline.estimate_minutes
  const hydrate = (value: Task) => { setBaseline(value); setTitle(value.title); setDescription(value.description); setDate(value.scheduled_date ?? ''); setEstimate(value.estimate_minutes) }
  useEffect(() => { if (detail.data && !dirty && !busy) hydrate(detail.data) }, [detail.data, dirty, busy])
  const latest = detail.data ?? baseline
  const deleted = detail.failure instanceof APIError && detail.failure.status === 404
  const isReadOnly = deleted || latest.plan_status === 'achieved' || latest.plan_status === 'abandoned' || latest.version_status === 'superseded' || latest.version_status === 'canceled'
  const stale = dirty && (latest.version !== baseline.version || isReadOnly || (baseline.version_status && latest.version_status !== baseline.version_status))
  const isDraft = latest.version_status === 'draft'
  const activeSession = dashboard.data ? dashboard.data.running_session?.session ?? null : running
  const isRunning = activeSession?.task_id === task.id
  const noteStale = Boolean(sessionNote && noteSessionID !== (isRunning ? activeSession.id : null))
  const reloadLatest = async () => {
    if ((dirty || sessionNote) && !window.confirm('重新加载会放弃尚未保存的输入，继续吗？')) return
    const value = await detail.reload()
    if (value) { hydrate(value); setSessionNote(''); setNoteSessionID(null); setNotice('') }
  }
  const execute = async (job: () => Promise<unknown>, saving = false, preservesNote = false) => {
    if (isReadOnly) { setNotice('任务已删除、计划已结束或版本已切换，无法继续修改。'); return }
    if (saving && stale) { setNotice('数据已在其他客户端更新，请先核对最新数据。'); return }
    if (!saving && (dirty || (sessionNote && !preservesNote)) && !window.confirm('此操作会关闭任务详情，尚未保存的修改将被放弃，继续吗？')) return
    setBusy(true); setNotice('')
    try { await job(); onChanged() } catch (reason) { setNotice(reason instanceof Error ? reason.message : '操作失败，请稍后重试') } finally { setBusy(false) }
  }
  const save = () => execute(() => api(token, `/tasks/${task.id}`, { method: 'PATCH', body: JSON.stringify({ version: baseline.version, title, description, estimate_minutes: estimate, scheduled_date: date || undefined, clear_scheduled_date: !date }) }), true)
  const blocked = busy || isReadOnly
  return <div className="drawer-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose() }}>
    <aside className="task-drawer">
      <div className="drawer-head"><div><span>任务详情</span><strong>{latest.plan_title || '学习计划'}</strong></div><button onClick={onClose} aria-label="关闭任务详情"><X size={18} /></button></div>
      <div className="drawer-body">
        <div className="drawer-status-row"><span className="plan-badge tone-blue">{latest.plan_mode === 'sequence' ? '按课时' : '按日期'}</span><span className="task-state">{isDraft ? '草稿任务' : latest.status}</span></div>
        {(stale || deleted || isReadOnly) && <div className="sync-edit-warning" role="alert"><p>{deleted ? '任务已被删除。你的输入仍保留，但无法保存。' : isReadOnly ? '计划或版本已变为只读，你的输入仍保留。' : '数据已在其他客户端更新，尚未保存的输入已保留。'}</p>{!deleted && <button className="button" onClick={() => void reloadLatest()}>重新加载最新数据</button>}</div>}
        <input className="drawer-title-input" aria-label="任务名称" disabled={isReadOnly} value={title} onChange={(event) => setTitle(event.target.value)} />
        <div className="drawer-fields">
          <label><span>安排日期</span><input type="date" disabled={isReadOnly || latest.plan_mode === 'sequence'} value={date} onChange={(event) => setDate(event.target.value)} /></label>
          <label><span>预计投入（分钟）</span><input type="number" min="0" disabled={isReadOnly} value={estimate} onChange={(event) => setEstimate(Number(event.target.value))} /></label>
          <label><span>版本</span><strong>{baseline.version}</strong></label><label><span>状态</span><strong>{isDraft ? '尚未激活' : latest.status}</strong></label>
        </div>
        <section className="drawer-section"><h3>任务说明</h3><textarea aria-label="任务说明" disabled={isReadOnly} value={description} onChange={(event) => setDescription(event.target.value)} /></section>
        {!isDraft && <section className="drawer-section"><h3>执行计时</h3>
          {isRunning ? <div className="activity-row"><span className="activity-dot active-dot" /><div><strong>正在计时</strong><span>开始于 {new Date(activeSession.started_at).toLocaleString('zh-CN')}</span></div></div> : <p>开始后由服务器记录实际投入时间，同一时间只能计时一项任务。</p>}
          {noteStale && <p className="drawer-error">计时已在其他客户端变更，未提交的备注仍保留。请核对后重新编辑或复制保存。</p>}
          {(isRunning || sessionNote) && <textarea className="session-note" aria-label="计时备注" value={sessionNote} onChange={(event) => { setSessionNote(event.target.value); setNoteSessionID(isRunning ? activeSession.id : null) }} placeholder="结束计时时保存一条简短记录（可选）" />}
          <div className="session-history">{history.error && <span className="drawer-error">{history.error}</span>}{history.data?.map((session) => <div className="session-record" key={session.id}><span className={session.status === 'finished' ? 'activity-dot' : 'activity-dot muted-dot'} /><div><strong>{session.status === 'finished' ? `学习 ${Math.max(1, Math.round(session.duration_seconds / 60))} 分钟` : session.status === 'running' ? '正在计时' : '已丢弃'}</strong><span>{new Date(session.started_at).toLocaleString('zh-CN')}{session.note ? ` · ${session.note}` : ''}</span></div></div>)}{history.data && !history.data.length && <span className="muted-copy">暂无历史计时记录</span>}</div>
        </section>}
        {detail.error && !deleted && <p className="drawer-error">{detail.error}，最新数据暂不可用。</p>}{notice && <p className="drawer-error">{notice}</p>}
      </div>
      <div className="drawer-actions">
        <button className="button" disabled={blocked || Boolean(stale)} onClick={save}>保存修改</button>
        {isDraft ? <button className="button button-danger" disabled={blocked} onClick={() => execute(() => api(token, `/tasks/${task.id}`, { method: 'DELETE' }))}>删除草稿任务</button> : <>
          {latest.status === 'done' ? <button className="button" disabled={blocked} onClick={() => execute(() => api(token, `/tasks/${task.id}/reopen`, { method: 'POST' }))}>重新打开</button> : latest.status !== 'canceled' && <button className="button" disabled={blocked} onClick={() => execute(() => api(token, `/tasks/${task.id}/complete`, { method: 'POST' }))}>完成任务</button>}
          {latest.status !== 'done' && latest.status !== 'canceled' && !isRunning && <button className="button button-danger" disabled={blocked} onClick={() => execute(() => api(token, `/tasks/${task.id}/cancel`, { method: 'POST' }))}>取消任务</button>}
          {isRunning ? <><button className="button" disabled={blocked} onClick={() => execute(() => api(token, `/sessions/${activeSession.id}/discard`, { method: 'POST' }))}>丢弃计时</button><button className="button button-primary" disabled={blocked || noteStale} onClick={() => execute(() => api(token, `/sessions/${activeSession.id}/finish`, { method: 'POST', body: JSON.stringify({ note: sessionNote }) }), false, true)}><Clock3 size={15} />结束计时</button></> : <button className="button button-primary" disabled={blocked || latest.status === 'done' || latest.status === 'canceled'} onClick={() => execute(() => api(token, `/tasks/${task.id}/sessions`, { method: 'POST' }))}><Timer size={15} />开始计时</button>}
        </>}
      </div>
    </aside>
  </div>
}
