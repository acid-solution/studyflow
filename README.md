# StudyFlow

StudyFlow 是面向个人学习执行的多用户平台。用户可以把长期目标递归拆成分目标，在任意目标下同时推进多个按日期或按课时的计划，通过版本草稿调整路线，并用任务、计时与改期记录生成周度复盘。

项目使用独立的 `shared-auth` 认证服务。StudyFlow 只保存 JWT 中的稳定 UUID，不保存账号、密码或本地 Session。

## 已实现的业务闭环

- 不限层数的目标树，支持移动、达成、放弃和 `ready_to_complete` 完成提示。
- 同一目标下多个并行计划；计划分为 `calendar` 和 `sequence` 两种固定模式。
- 每个计划独立维护 V1、V2 等版本；草稿从当前版本复制未完成任务，激活时保留历史版本和执行记录。
- 阶段与任务编排，日期任务只排到天，课时计划按顺序返回下一课。
- 首页按“逾期、今天、按课时、未来”返回任务。
- 任务完成、重新打开、取消、改期和整数版本乐观锁。
- 服务端计时，同一用户同时只能运行一个计时；完成任务会自动结束该任务的运行中计时。
- 周报统计完成数、预算与实际投入、按期完成率、逾期数、改期次数、每日投入和各计划投入。
- MySQL 版本化 Migration、UUID 用户隔离、shared-auth JWKS 本地验签。
- React 前端已接入真实 API；计划导入与 MCP 页面明确保留为后续功能。

## 技术栈

| 层次 | 技术 |
| --- | --- |
| 前端 | React 19、TypeScript、Vite、Vitest |
| API | Go、Gin、GORM |
| 数据库 | MySQL 8.4、Goose Migration |
| 身份 | shared-auth、RS256 JWT、JWKS |
| 工程 | Docker Compose、GitHub Actions |

首轮没有引入 Redis。周报和工作台直接从 MySQL 查询，后续只有在真实性能数据证明需要时才增加缓存。

## 核心数据关系

```text
Goal（可递归）
└─ Plan（同一目标可并行多个）
   └─ PlanVersion（草稿 / 生效 / 历史）
      ├─ Milestone
      └─ Task
         └─ StudySession
```

所有业务表均保存 `user_id`。每次查询同时限制资源 ID 与当前用户；访问其他用户资源和访问不存在资源统一返回 404。

## 目录

```text
studyflow/
├─ frontend/                 React 前端
├─ migrations/               Goose MySQL 迁移
├─ internal/
│  ├─ config/                环境配置
│  ├─ database/              MySQL 与迁移启动
│  ├─ identity/              JWKS 缓存和 JWT 校验
│  ├─ middleware/            请求 ID 与结构化日志
│  ├─ model/                 持久化模型
│  ├─ response/              统一响应
│  └─ studyflow/             领域服务与 HTTP 接口
├─ compose.yaml
├─ Dockerfile
└─ main.go
```

## 本地开发

### 1. 启动 shared-auth

StudyFlow 默认连接 `http://127.0.0.1:18082`。shared-auth 需要为 `studyflow` 客户端允许：

```text
http://127.0.0.1:5174
http://127.0.0.1:8081
```

### 2. 启动 MySQL

```powershell
Copy-Item .env.compose.example .env.compose
docker compose --env-file .env.compose up -d mysql
```

### 3. 启动 API

```powershell
Copy-Item .env.example .env
go run .
```

服务启动时会自动执行 `migrations` 中尚未应用的 Goose Migration。

### 4. 启动前端

```powershell
Set-Location frontend
npm ci
npm run dev
```

访问 `http://127.0.0.1:5174`。

## Docker Compose 完整启动

确保宿主机上的 shared-auth 已启动，然后执行：

```powershell
Copy-Item .env.compose.example .env.compose
docker compose --env-file .env.compose up --build -d
```

访问 `http://127.0.0.1:8081`。生产构建由同一个 Go 服务提供前端静态资源和 `/api/v1` API。

## 身份校验

业务接口要求 `Authorization: Bearer <shared-auth access token>`。服务严格验证 RS256、`iss=shared-auth`、`aud=studyflow`、`kid`、UUID 格式的 `sub`/`sid`、`exp` 和 `iat`。签发给 `jobpilot` 的令牌不能调用 StudyFlow。

## API 概览

成功响应统一为 `{"code":0,"message":"success","data":{}}`。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/health/live` | 进程存活 |
| GET | `/health/ready` | 数据库就绪 |
| GET | `/api/v1/goals/tree` | 递归目标树 |
| POST | `/api/v1/goals` | 创建目标或分目标 |
| PATCH | `/api/v1/goals/:id` | 编辑或移动目标 |
| POST | `/api/v1/goals/:id/complete` | 确认达成目标 |
| POST | `/api/v1/goals/:id/abandon` | 放弃目标 |
| POST | `/api/v1/goals/:id/plans` | 创建计划与 V1 草稿 |
| GET | `/api/v1/plans` | 查询计划 |
| GET/PATCH | `/api/v1/plans/:id` | 计划详情或编辑 |
| POST | `/api/v1/plans/:id/versions` | 从生效版复制新草稿 |
| POST | `/api/v1/plan-versions/:id/activate` | 激活草稿 |
| POST | `/api/v1/plan-versions/:id/milestones` | 新建阶段 |
| POST | `/api/v1/plan-versions/:id/tasks` | 新建任务 |
| POST | `/api/v1/plan-imports` | 批量导入计划树（幂等） |
| PATCH/DELETE | `/api/v1/milestones/:id` | 编辑或删除草稿阶段 |
| GET/PATCH/DELETE | `/api/v1/tasks`、`/api/v1/tasks/:id` | 筛选、编辑或删除草稿任务 |
| POST | `/api/v1/tasks/:id/complete` | 完成任务 |
| POST | `/api/v1/tasks/:id/reopen` | 重新打开 |
| POST | `/api/v1/tasks/:id/cancel` | 取消任务 |
| GET | `/api/v1/tasks/:id/sessions` | 读取任务执行记录 |
| POST | `/api/v1/tasks/:id/sessions` | 开始计时 |
| POST | `/api/v1/sessions/:id/finish` | 结束计时 |
| POST | `/api/v1/sessions/:id/discard` | 丢弃误开计时 |
| GET | `/api/v1/dashboard/today` | 今日工作台 |
| GET | `/api/v1/reviews/weekly` | 周度复盘 |
| GET/PATCH | `/api/v1/preferences` | 用户偏好 |

## 计划树批量导入

`POST /api/v1/plan-imports` 在一次事务里建出计划、V1 草稿、阶段和任务，用于 JobPilot 生成的学习路线导入。请求头必须带 `Idempotency-Key`（8 到 200 个可打印 ASCII 字符），作用域是当前用户。

任务嵌在所属阶段里，位置由服务端按数组顺序生成，客户端不能指定：

```json
{
  "goal_id": "目标 UUID",
  "title": "力扣冲刺",
  "mode": "calendar",
  "weekly_capacity_minutes": 600,
  "milestones": [
    { "title": "数组与哈希", "outcome": "掌握三类模板",
      "tasks": [{ "title": "三数之和", "estimate_minutes": 60, "scheduled_date": "2026-03-03" }] }
  ],
  "tasks": [{ "title": "未分阶段任务" }]
}
```

幂等契约：

- 同一个键加同一份载荷重复提交，返回第一次建好的那棵树，响应里 `replayed` 为 `true`，不会产生第二份数据。
- 摘要算在**规范化之后**的载荷上，所以重试时多写空格、省略空字段不影响判定；同一个键配上真正不同的载荷返回 409 `idempotency_key_conflict`。
- 任何校验失败都会整批回滚，包括已经写入的阶段和任务，同时释放幂等键，可以用同一个键重试。

服务端不自动重试死锁或锁等待超时：客户端可能已经超时，幂等键才是重试机制。

## MCP 工具

`POST /mcp` 暴露三个 MCP 工具，走 Streamable HTTP，供 JobPilot 这类 Agent 调用。它和 REST API 挂在同一个进程、同一个端口上，并且调用**同一套应用服务**——校验、事务边界和乐观锁都只有一份实现，不会出现两套业务规则。

鉴权与 REST API 完全一致：同一个 `Authorization: Bearer <shared-auth access token>`，同一套 RS256/JWKS 校验，audience 仍然是 `studyflow`。所以签发给 `jobpilot` 的令牌同样调不动这些工具。

| 工具 | 用途 |
| --- | --- |
| `import_plan_tree` | 一次事务导入计划树，参数与 `/api/v1/plan-imports` 相同，另加一个调用方生成的 `idempotency_key` |
| `query_progress` | 查询生效计划的完成进度和任务列表，任务带上乐观锁版本号 |
| `reschedule_task` | 修改或取消某个任务的计划日期，必须回传版本号 |

两个设计点：

- **幂等键是工具参数，不是 HTTP 头。** 调用方是 Agent，它自己生成并在重试时复用这个键；头属于传输层，工具契约不该依赖它。
- **改期保留乐观锁。** `version` 必填，不在 MCP 层偷偷"读最新版再写"——那样并发改期会静默丢更新，而静默丢更新比报错难查得多。

会话是无状态的（`Stateless`）：每个请求各带自己的令牌，这些工具也不会反向调用客户端，因此没有会话劫持面。

## 读缓存

`GET /goals/tree` 读得最频繁——目标页每次打开要读，MCP 的 `query_progress` 每次调用也要读——而它要跑四次查询再拼树，还要递归算一遍 `ready_to_complete`。这一条走 Redis。

**失效按代数做，不是删 key。** 每个用户的当前代数会进 key：

```text
sf:v1:gen:<user_id>              ← 任何写操作 INCR 一次
sf:v1:goaltree:<user_id>:<gen>   ← 值：目标树 JSON
```

会改动目标树的写路径有十七处（目标、计划、版本、阶段、任务、计时、导入）。逐个删 key 就是十七次漏掉的机会，而且 `dashboard` 那类 key 还带日期，删之前得先算出是哪天。改成代数之后，失效只有一处，旧 key 自然不可达，靠 TTL 回收。

**缓存挂了不影响业务。** 所有 Redis 操作都是 fail-open：连不上、超时或解码失败一律当成未命中，直接回源 MySQL。`CACHE_ENABLED=false` 就是纯 MySQL 版本，启动时 ping 不通也会自动退回这个模式。每次操作还有 200ms 超时，避免卡住的 Redis 拖慢请求。

TTL 默认 30s，带 ±10% 抖动防止同一批 key 同时过期把压力打回数据库。

**不放 Redis 的东西**（边界比用法更能说明问题）：

- **幂等回执**（`plan_imports`）留在 MySQL。幂等保证要持久，放 Redis 会因为过期或重启丢失，正好破坏它要保证的东西。
- **单计时器约束**（`uk_sessions_one_running`）留在 MySQL 唯一键。这是不变量不是锁，换成 Redis 分布式锁只会弱化它。
- **任务和计划数据本身**不缓存，只在聚合读那一层缓存。

配置：

```env
CACHE_ENABLED=true
REDIS_ADDR=127.0.0.1:6379
REDIS_PASSWORD=
REDIS_DB=0
CACHE_TTL=30s
```

compose 里的 redis 故意关掉了持久化（`--save "" --appendonly no`）：它是纯缓存，重启后全部回源即可；而代数计数器和它保护的数据在同一个实例里，所以重启不可能让旧数据复活。

## 验证

```powershell
go test ./...

Set-Location frontend
npm test
npm run typecheck
npm run build
```

数据库集成测试默认跳过。提供一个空测试数据库，并设置 `TEST_MYSQL_DSN` 后运行：

```powershell
go test ./internal/studyflow -run TestPlanningExecutionAndReviewIntegration -v
```

测试覆盖递归目标、循环引用阻止、并行计划、版本复制与激活、用户隔离、首页分类、乐观锁、单计时约束和周报聚合。
