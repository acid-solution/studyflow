export type PlanMode = 'calendar' | 'sequence'
export type TaskStatus = 'todo' | 'in_progress' | 'done' | 'canceled'
// Who put a plan or task there: a person using the app, or an agent that imported it.
export type TaskSource = 'user' | 'agent'

export type PlanSummary = {
  id: string
  goal_id: string
  title: string
  description: string
  mode: PlanMode
  source: TaskSource
  active_version_id: string | null
  active_version_no: number | null
  draft_version_id: string | null
  done_tasks: number
  total_tasks: number
}

export type GoalNode = {
  id: string
  parent_goal_id: string | null
  title: string
  description: string
  success_criteria: string
  target_date: string | null
  status: 'active' | 'achieved' | 'abandoned'
  version: number
  ready_to_complete: boolean
  plans: PlanSummary[]
  children: GoalNode[]
}

export type Task = {
  id: string
  plan_id?: string
  plan_title?: string
  plan_mode?: PlanMode
  version_status?: 'draft' | 'active' | 'superseded' | 'canceled'
  plan_version_id: string
  milestone_id: string | null
  title: string
  description: string
  estimate_minutes: number
  scheduled_date: string | null
  position: number
  status: TaskStatus
  version: number
  source: TaskSource
  completed_at: string | null
  is_overdue: boolean
}

export type Milestone = { id: string; title: string; outcome: string; position: number; tasks: Task[] }
export type PlanVersion = {
  id: string
  version_no: number
  status: 'draft' | 'active' | 'superseded' | 'canceled'
  weekly_capacity_minutes: number
  start_date: string | null
  end_date: string | null
  structure_revision: number
  milestones: Milestone[]
  unassigned_tasks: Task[]
}
export type PlanDetail = {
  plan: { id: string; goal_id: string; active_version_id: string | null; title: string; description: string; mode: PlanMode; source: TaskSource; created_at: string }
  versions: PlanVersion[]
}

export type Session = { id: string; task_id: string; started_at: string; ended_at: string | null; duration_seconds: number; note: string; status: string }
export type Dashboard = {
  date: string
  overdue: Task[]
  today: Task[]
  completed_today: Task[]
  sequence_plans: Array<{ plan_id: string; plan_title: string; done_tasks: number; total_tasks: number; next_task: Task | null }>
  future: Task[]
  running_session: { session: Session; task: Task } | null
}

export type Preferences = { timezone: string; week_start: 'monday' | 'sunday'; default_plan_mode: PlanMode; show_completed_today: boolean }
export type WeeklyReview = {
  week_start: string
  week_end: string
  completed_tasks: number
  planned_minutes: number
  actual_duration_seconds: number
  on_time_completed: number
  scheduled_tasks: number
  on_time_rate: number
  current_overdue: number
  reschedule_count: number
  daily: Array<{ date: string; duration_seconds: number }>
  plans: Array<{ plan_id: string; plan_title: string; planned_minutes: number; actual_duration_seconds: number; completed_tasks: number; reschedule_count: number }>
}

export type ImportTask = { title: string; description?: string; estimate_minutes?: number; scheduled_date?: string | null }
export type ImportDraft = {
  goal_id?: string
  title: string
  description?: string
  mode: PlanMode
  weekly_capacity_minutes?: number
  start_date?: string | null
  end_date?: string | null
  milestones?: Array<{ title: string; outcome?: string; tasks?: ImportTask[] }>
  tasks?: ImportTask[]
}
export type PlanImportResult = {
  import_id: string
  replayed: boolean
  plan_id: string
  plan_version_id: string
  plan: PlanDetail
}

export type PlanImportView = {
  import_id: string
  idempotency_key: string
  plan_id: string
  plan_title: string
  plan_version_id: string
  milestones: number
  tasks: number
  created_at: string
}
