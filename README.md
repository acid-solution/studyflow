# StudyFlow

StudyFlow 是多用户学习规划与执行平台：把长期目标组织为并行计划，保留每套计划版本的独立任务与进度，记录实际投入，并用周度复盘帮助调整安排。

它可以独立使用，也可以接收 JobPilot 经用户确认的学习路线。JobPilot 负责规划，StudyFlow 负责执行。

## 核心功能

- 递归目标树，同一目标下可以有多个并行学习计划。
- 每份计划有明确的计划目标；支持空白草稿、复制版本、激活和历史切换。
- 每个版本独立选择按日期或按课时推进，保留自己的任务状态。
- 今日工作台、任务完成与改期、服务端计时、周度复盘。
- 放弃项可继续阻塞上层或从判断中排除，支持账号默认选择。
- 事务批量导入、幂等回执、MCP 工具和 JobPilot 路线接入。
- 共享认证、用户隔离、乐观锁、Redis 聚合读缓存、可靠多端变更通知。

任务完成、计划达成和目标达成由用户分别确认。导入只建立草稿，不自动激活。

## 快速开始

需要 Go 1.26.4+、Node.js 24.18+、Docker Compose，以及独立运行的 shared-auth。首次配置与认证 Origin 见[开发指南](docs/DEVELOPMENT.md)。

在仓库根目录创建自己的 `.env`、`.env.compose`，填写一致的 MySQL 连接与密码，然后启动：

~~~powershell
docker compose --env-file .env.compose up -d mysql redis
go run .
~~~

另开终端，从仓库根目录运行前端：

~~~powershell
Set-Location frontend
npm ci
npm run dev
~~~

访问 `http://127.0.0.1:5174`。API 默认 `http://127.0.0.1:8081`，认证默认 `http://127.0.0.1:18082`。API 启动会执行 Goose 数据库迁移。

## 技术与系统边界

React、TypeScript、Vite、Vitest；Go、Gin、GORM、MySQL、Goose、Redis；Docker Compose、GitHub Actions。

StudyFlow 业务库使用 MySQL，Redis 只缓存聚合读。独立 shared-auth 使用自己的 PostgreSQL，业务服务只保存稳定用户 UUID。MCP 和 HTTP 共用同一应用服务及事务规则。

## 文档入口

| 需要了解什么 | 文档 |
| --- | --- |
| 全部文档及维护约定 | [文档导航](docs/README.md) |
| 产品使用与行为边界 | [产品说明](docs/PRODUCT.md) |
| 目标、计划和版本的含义 | [业务词典](GLOSSARY.md) |
| 分层、数据流与关键取舍 | [架构说明](ARCHITECTURE.md) |
| 当前领域约束与状态 | [领域规则](STUDYFLOW_V2_PLAN.md) |
| 环境、配置与排错 | [开发指南](docs/DEVELOPMENT.md) |
| HTTP、MCP 与跨系统导入 | [API](docs/API.md)、[MCP](docs/MCP.md)、[路线导入](docs/learning-route-import.md) |
| 部署、升级与恢复 | [部署说明](docs/DEPLOYMENT.md)、[CI/CD](docs/ci-cd.md) |
| 验证方法与结果边界 | [验证指南](docs/VALIDATION.md) |

文档核对日期为 2026-10-17；测试和功能存在不等于当前线上环境已完成所有业务验收。
