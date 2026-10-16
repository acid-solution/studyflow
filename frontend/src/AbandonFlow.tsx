import { FormEvent, useCallback, useEffect, useRef, useState } from 'react'
import { Check, Circle, Info } from 'lucide-react'
import { api } from './api'
import { EditorDialog, errorMessage } from './CreationDialogs'
import type { Preferences } from './types'

type ParentEffect = 'block' | 'exclude'

// A saved default is an explicit opt-in to skip the dialog. Do not fall back to
// an automatic write if preferences are unavailable or the value is unknown.
export default function AbandonFlow({ token, kind, id, title, hasParent, onClose, onAbandoned }: {
  token: string; kind: 'goal' | 'plan'; id: string; title: string; hasParent: boolean
  onClose: () => void; onAbandoned: (notice: string) => void
}) {
  const [effect, setEffect] = useState<ParentEffect>('block')
  const [remember, setRemember] = useState(false)
  const [showDialog, setShowDialog] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [loading, setLoading] = useState(true)
  const pending = useRef(false)
  const callbacks = useRef({ onClose, onAbandoned })
  callbacks.current = { onClose, onAbandoned }
  const label = kind === 'goal' ? '目标' : '计划'
  const perform = useCallback(async (choice: ParentEffect, saveDefault: boolean) => {
    if (pending.current) return
    pending.current = true; setBusy(true); setError('')
    try {
      await api(token, `/${kind === 'goal' ? 'goals' : 'plans'}/${id}/abandon`, { method: 'POST', body: JSON.stringify(hasParent ? { parent_effect: choice } : {}) })
      let notice = `已放弃“${title}”。`
      if (saveDefault) {
        try {
          await api(token, '/preferences', { method: 'PATCH', body: JSON.stringify({ default_abandon_parent_effect: choice }) })
          notice += '以后将直接使用此选择，可在设置中修改。'
        } catch {
          // The business action succeeded. Never retry it just because saving
          // the separate account preference failed.
          notice += '默认选择未能保存，请到设置中重试。'
        }
      }
      callbacks.current.onAbandoned(notice)
    } catch (reason) { setError(errorMessage(reason)) }
    finally { pending.current = false; setBusy(false) }
  }, [token, kind, id, hasParent, title])

  const performRef = useRef(perform)
  performRef.current = perform
  useEffect(() => {
    let canceled = false
    setLoading(true); setError('')
    void api<Preferences>(token, '/preferences').then(preferences => {
      if (canceled) return
      const choice = preferences.default_abandon_parent_effect
      if (choice === 'block' || choice === 'exclude') void performRef.current(choice, false)
      else setShowDialog(true)
    }).catch(reason => { if (!canceled) setError(errorMessage(reason)) })
      .finally(() => { if (!canceled) setLoading(false) })
    return () => { canceled = true }
  }, [token, kind, id, attempt])

  const close = () => { if (!pending.current) callbacks.current.onClose() }
  const submit = (event: FormEvent) => { event.preventDefault(); void perform(effect, hasParent && remember) }
  if (!showDialog) return <div className="abandon-feedback" role={error ? 'alert' : 'status'}>
    {error ? <><span>未能放弃{label}：{error}</span><button className="button" onClick={() => setAttempt(value => value + 1)}>重试</button><button className="button" onClick={close}>取消</button></> : <span>{loading && !busy ? '正在读取放弃偏好…' : `正在放弃${label}…`}</span>}
  </div>
  return <EditorDialog title={`放弃${label}`} description={`确定不再推进“${title}”吗？内容和已有记录会保留，之后可以恢复。`} onClose={close}>
    <form className="editor-form" onSubmit={submit}>
      <div className="editor-body">
        {hasParent ? <>
          <fieldset className="abandon-options" disabled={busy}><legend>这项内容还需要计入上层目标吗？</legend>
            <label className={effect === 'exclude' ? 'abandon-option is-selected' : 'abandon-option'}>
              <input type="radio" name="parent-effect" value="exclude" checked={effect === 'exclude'} onChange={() => setEffect('exclude')} />
              <span className="abandon-choice-mark">{effect === 'exclude' ? <Check size={16} /> : <Circle size={18} />}</span>
              <span><strong>从上层完成判断中排除</strong><small>不再把它作为必要子项，上层可以根据其余子项判断完成。</small></span>
            </label>
            <label className={effect === 'block' ? 'abandon-option is-selected' : 'abandon-option'}>
              <input type="radio" name="parent-effect" value="block" checked={effect === 'block'} onChange={() => setEffect('block')} />
              <span className="abandon-choice-mark">{effect === 'block' ? <Check size={16} /> : <Circle size={18} />}</span>
              <span><strong>仍作为必要子项，继续阻塞上层</strong><small>暂时不做，但上层仍会提示这项内容尚未达成。</small></span>
            </label>
          </fieldset>
          <label className="editor-checkbox abandon-remember"><input type="checkbox" checked={remember} disabled={busy} onChange={event => setRemember(event.target.checked)} /><span>以后按此选择直接放弃，不再弹窗<small>对目标和学习计划共用，可在「设置 → 放弃操作」中修改。</small></span></label>
        </> : <div className="creation-note">这是顶层目标，没有上层完成判断需要处理。</div>}
        <p className="abandon-explanation"><Info size={16} /><span>{kind === 'goal' ? '只改变这个目标的状态，不会自动放弃其下的分目标或计划。' : '放弃后计划暂停执行，任务会从今日安排中移出；恢复后可继续。'}</span></p>
        {error && <p className="import-error" role="alert">{error}</p>}
      </div>
      <footer className="editor-footer"><button type="button" className="button" disabled={busy} onClick={close}>继续保留</button><button className="button button-danger" disabled={busy}>{busy ? '处理中…' : `确认放弃${label}`}</button></footer>
    </form>
  </EditorDialog>
}
