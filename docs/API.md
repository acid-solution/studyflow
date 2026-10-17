# StudyFlow HTTP API

核对日期：2026-10-17。路由来源为 [http.go](../internal/studyflow/http.go)与[通知路由](../internal/studyflow/notification_http.go)。业务 JSON 使用 `{"code":0,"message":"success","data":{}}` 包装；SSE 另见[通知协议](notifications.md)。

## 身份与错误

除健康检查外，普通接口使用 `Authorization: Bearer <StudyFlow Access Token>`。服务验证 shared-auth 的 RS256、issuer、audience、期限及用户/会话标识。身份从令牌取得，不接受客户端自定用户。

| HTTP 状态 | 处理 |
| --- | --- |
| 400 | 参数或格式无效，保留输入并核对字段 |
| 401 | 令牌、受众或认证无效 |
| 404 | 当前用户范围内不存在资源 |
| 409 | 版本、状态、幂等或完成确认冲突，读取具体错误 |
| 500 | 服务端故障，不假定写入成功或换键重试 |

目标没有必要子项返回 `goal_has_no_required_children`。存在未达成内容时，先返回 `completion_confirmation_required` 和摘要；用户核对后通过 `acknowledge_incomplete` 再明确确认。恢复前祖先不活跃返回 `ancestor_not_active`。

## 路由

以下业务路径统一加 `/api/v1`。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/health/live`、`/health/ready`（不加前缀） | 进程、数据库健康 |
| GET | `/goals/tree` | 目标树与直属完成摘要 |
| POST | `/goals` | 新建目标或分目标 |
| PATCH | `/goals/:id` | 编辑、移动目标 |
| POST | `/goals/:id/complete`、`/abandon`、`/reopen` | 达成、放弃、恢复 |
| PATCH | `/goals/:id/completion-policy` | 放弃项的上层影响 |
| POST | `/goals/:id/plans` | 创建计划及 V1 草稿 |
| GET | `/plans`、`/plans/:id` | 列表、详情 |
| PATCH | `/plans/:id` | 计划属性 |
| POST | `/plans/:id/complete`、`/abandon`、`/reopen` | 计划状态 |
| PATCH | `/plans/:id/completion-policy` | 上层影响 |
| POST | `/plans/:id/versions` | 空白或复制草稿 |
| PATCH | `/plan-versions/:id` | 草稿设置 |
| POST | `/plan-versions/:id/cancel`、`/activate` | 取消草稿、激活或切换版本 |
| POST | `/plan-versions/:id/milestones`、`/tasks` | 草稿编排 |
| PATCH/DELETE | `/milestones/:id` | 编辑/删除阶段 |
| GET | `/tasks`、`/tasks/:id` | 筛选与最新详情 |
| PATCH/DELETE | `/tasks/:id` | 编辑或删除任务，受版本状态约束 |
| POST | `/tasks/:id/complete`、`/reopen`、`/cancel` | 完成、恢复、取消 |
| GET/POST | `/tasks/:id/sessions` | 查询执行记录、开始计时 |
| POST | `/sessions/:id/finish`、`/discard` | 结束或丢弃计时 |
| POST/GET | `/plan-imports` | 批量导入、查询回执 |
| GET | `/dashboard/today`、`/reviews/weekly` | 今日与周报 |
| GET/PATCH | `/preferences` | 时区、周起始日、默认模式和放弃偏好 |
| GET | `/events` | 账号变更 SSE |

表中同一行缩写的后缀沿用前一完整资源路径，例如 `/goals/:id/abandon`。精确请求结构以服务输入类型为准。

## 计划树导入示例

先创建一个属于当前用户的目标，替换示例中的 UUID。请求头带 `Idempotency-Key`，8–200 个可打印 ASCII 字符。

~~~json
{
  "goal_id": "替换为当前用户目标UUID",
  "title": "基础复现",
  "objective": "能够独立复现数组与哈希的常见题型",
  "success_criteria": "完成限时复现并解释边界",
  "mode": "calendar",
  "weekly_capacity_minutes": 300,
  "milestones": [
    {
      "title": "数组与哈希",
      "outcome": "完成一轮独立复现",
      "tasks": [
        {"title": "复现一道哈希题", "estimate_minutes": 45}
      ]
    }
  ],
  "tasks": [{"title": "整理本轮问题", "estimate_minutes": 15}]
}
~~~

`scheduled_date` 可用于日期任务，格式 `YYYY-MM-DD`；省略表示未安排。课时模式 `sequence` 不允许任务日期。位置按数组顺序生成，每次最多 50 阶段、200 任务。

整棵计划树与回执同事务。摘要基于规范化载荷，相同用户、相同键、相同内容重放原结果，`replayed=true`；不同内容返回幂等冲突。失败不保留半棵树，也不永久占用键。

`GET /plan-imports` 返回当前用户最近 50 条事务导入记录，数量读取对应版本当前规模，不是当时快照。该列表不是所有业务审计或在途请求状态，缺回执时仍需按原幂等请求核实。

## 并发与来源

任务等更新按对应输入回传 `version`；Plan 属性使用自己的修订机制。冲突后不替旧表单自动填入新版本号。

计划与任务分别保存 `source=user|agent`。通过 MCP 导入为 agent，用户后续手动新增任务仍为 user，不从旧回执反推整份计划的所有任务来源。

MCP、外部路线包和通知分别见 [MCP](MCP.md)、[路线导入](learning-route-import.md)、[通知](notifications.md)。
