# StudyFlow 架构

StudyFlow 使用一个 Go API 服务提供 HTTP、MCP、SSE 和构建后的前端；MySQL 保存业务事实，Redis 缓存聚合查询。核对日期：2026-10-17。

## 系统与分层

~~~mermaid
flowchart TD
  Web[React 页面] --> HTTP[HTTP Handler]
  Caller[JobPilot 或 MCP 客户端] --> MCP[MCP Handler]
  HTTP --> Service[应用服务]
  MCP --> Service
  Service --> Repo[Repository 接口]
  Repo --> MySQL[(MySQL)]
  Service --> Cache[(Redis 聚合读)]
  MySQL --> Hub[通知投递器]
  Hub --> SSE[SSE 页面连接]
  Web --> Auth[shared-auth]
  HTTP --> JWKS[JWKS 本地验签]
~~~

`main.go` 负责组装。Handler 处理传输、身份和输入，Service 处理领域规则与事务编排，Repository 负责 GORM、SQL、行锁和唯一键错误。Service 不直接依赖 GORM，HTTP 与 MCP 共用同一套服务。

[internal/studyflow/repository.go](internal/studyflow/repository.go)同时定义持久化端口与适配；事务通过 `WithinTransaction` 把同一事务中的 Repository 传给 Service 回调。这里是单体内的依赖边界，不代表已经拆成多个业务微服务。

## 数据所有权

| 所有者 | 数据 |
| --- | --- |
| StudyFlow / MySQL | 目标、计划、版本、阶段、任务、计时、改期、偏好、导入回执、通知事件 |
| shared-auth / PostgreSQL | 账号、密码、验证码、刷新会话、访问令牌签发 |
| JobPilot | 岗位与用户画像、推荐、学习路线版本及导入意图 |
| Redis | 用户代数与聚合结果，可丢失，不承担业务约束 |

业务身份从已验证 JWT 的 `sub` 取得，查询同时约束当前用户与资源。其他用户的资源与不存在资源统一返回 404。普通 StudyFlow 令牌与 JobPilot 代办令牌使用不同权限路径。

## 领域关系

~~~text
Goal
├─ Goal
└─ Plan
   ├─ PlanVersion V1
   └─ PlanVersion V2
      ├─ Milestone
      ├─ Task（可以不属于阶段）
      └─ Task
         ├─ StudySession
         └─ TaskScheduleEvent
~~~

任务属于计划版本而非整份计划；版本切换不搬迁进度。计划目标属于 Plan，推进方式和周预算属于 PlanVersion。来源路线与计划版本通过独立绑定和回执关联。字段与状态见[领域规则](STUDYFLOW_V2_PLAN.md)。

## 一次写操作如何完成

1. Handler 从认证上下文取得用户身份，校验请求格式。
2. Service 在事务内读取用户范围中的资源，检查父级状态、计划版本和乐观锁。
3. Repository 写业务数据，并在同一事务记录一条合并通知。
4. 提交成功后使相应聚合缓存失效，再返回结果。
5. 通知投递器读取已提交日志，通过当前实例的 SSE 连接提示页面刷新。

任务完成与结束该任务计时属于一个业务动作；导入回执与整棵计划树同成败。外部请求超时不说明事务失败，调用方通过幂等键核实。

## 并发与恢复

- 整数乐观锁防止旧表单覆盖新内容；客户端不应偷偷读取新版本号后重发旧输入。
- 数据库唯一约束保证同一账号只有一个运行中计时，幂等键按用户隔离。
- Redis 使用代数与条件回填；失效未确认或启动后先隔离回源，见[缓存](docs/CACHE.md)。
- 通知序号在用户流锁内分配并随业务事务提交；至少一次投递由客户端游标去重，见[通知](docs/notifications.md)。
- 业务数据、回执与可靠通知不依赖 Redis 持久性。

## 部署与迁移

应用启动调用 Goose 迁移，不采用 AutoMigrate。MySQL DDL 的实际提交行为不能由外层事务完全兜底；Down 迁移不等于数据备份，多实例迁移仍需部署协调。

单独 Compose 提供 MySQL、Redis、API；共享认证另行运行。联合栈和应用镜像发布由 JobPilot 的部署目录维护，详见[部署说明](docs/DEPLOYMENT.md)。

源码入口：[main.go](main.go)、[HTTP](internal/studyflow/http.go)、[MCP](internal/studyflow/mcp.go)、[领域服务](internal/studyflow/service.go)、[执行](internal/studyflow/execution.go)、[迁移](migrations)。
