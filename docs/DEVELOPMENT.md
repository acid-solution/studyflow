# StudyFlow 开发指南

以下命令从仓库根目录执行。版本依据 [go.mod](../go.mod)、[前端 package.json](../frontend/package.json)与锁文件；Go 1.26.4+、Node.js 24.18+。

## 准备认证

先按 [shared-auth README](https://github.com/acid-solution/shared-auth#readme)创建配置并在认证仓库执行 `docker compose up --build -d`，默认地址为 `http://127.0.0.1:18082`。允许 `studyflow` 客户端的实际 Origin，本地 Vite 为 `http://127.0.0.1:5174`，合并页面为 `http://127.0.0.1:8081`。

StudyFlow 不保存密码或刷新会话。普通接口要求签发给 `studyflow` 的访问令牌，JobPilot 的普通令牌不能通用。

## 创建本地配置

~~~powershell
if (-not (Test-Path -LiteralPath .env)) {
    Copy-Item -LiteralPath .env.example -Destination .env
}
if (-not (Test-Path -LiteralPath .env.compose)) {
    Copy-Item -LiteralPath .env.compose.example -Destination .env.compose
}
~~~

分别编辑两个文件；`.env.compose` 的数据库名、用户和密码应与 `.env` 的 `MYSQL_DSN` 一致。示例中的 `REDACTED`、`change_me_*` 都是占位值。

| 配置 | 默认或含义 |
| --- | --- |
| `APP_HOST`、`PORT` | `127.0.0.1`、`8081`；容器显式使用 `0.0.0.0` |
| `MYSQL_DSN` | 必填，使用应用账号和可解析时间参数 |
| `MIGRATIONS_DIR` | `migrations`，相对于运行目录 |
| `AUTH_JWKS_URL` | 默认本地认证的 `/.well-known/jwks.json` |
| `AUTH_ISSUER`、`AUTH_AUDIENCE` | `shared-auth`、`studyflow` |
| `JWKS_CACHE_TTL` | `10m` |
| `FRONTEND_URL` | `http://127.0.0.1:5174`，精确 Origin |
| `CACHE_ENABLED` | 程序默认 false，配置示例开启 |
| `REDIS_ADDR`、`REDIS_DB`、`CACHE_TTL` | `127.0.0.1:6379`、0、`30s` |
| `REDIS_PASSWORD` | 仅外部有密码 Redis；自带 Compose Redis 不需要 |
| `NOTIFICATION_RETENTION` | `168h`；提交后本地唤醒投递，无投递轮询间隔配置 |
| `STUDYFLOW_MCP_RESOURCE` | 路线代办令牌受众，需与 shared-auth 和 JobPilot 完全一致 |

程序读取当前目录的 `.env`，已有进程环境优先。Compose 变量与程序配置是不同入口，不能只改其中一份后假定另一份已更新。

## 启动

终端一：

~~~powershell
docker compose --env-file .env.compose up -d mysql redis
go run .
~~~

终端二，从仓库根目录：

~~~powershell
Set-Location frontend
npm ci
npm run dev
~~~

开发数据库宿主端口默认 3307，Redis 默认 6379；前端 5174，API 8081。首次启动执行 [Goose 迁移](../migrations)。`/health/live` 表示进程存活，`/health/ready` 检查数据库。

## 前端地址与合并构建

Vite 开发默认直接请求 `http://127.0.0.1:8081` 和本地认证。`VITE_API_URL`、`VITE_AUTH_URL` 是构建时变量，更改后需要重新构建；空字符串表示同源，不同于未设置时的默认值。

~~~powershell
Set-Location frontend
npm run build
Set-Location ..
go run .
~~~

Go 发现 `frontend/dist/index.html` 后提供静态页面。单独 Compose 构建默认写入本地服务地址；联合部署构建使用空地址，由 Caddy 转发同源 API 和认证。

## 验证与排错

测试与一次性数据库的配置见[验证指南](VALIDATION.md)。避免把开发或正式库传给集成测试。

| 现象 | 检查 |
| --- | --- |
| API 启动失败 | 运行目录、MYSQL_DSN、数据库健康、迁移版本 |
| 登录后 401 | 受众、issuer、JWKS、用户 UUID、令牌期限 |
| 浏览器跨域失败 | 页面 Origin 是否与 FRONTEND_URL 和认证客户端配置一致 |
| 任务修改冲突 | 原版本已变化，重新加载核对，不自动替换版本号 |
| 计划没有显示执行任务 | 是否激活草稿、是否为当前版本、所属目标或计划是否活跃 |
| Redis 连接正常但仍回源 | 启动或失效失败后的隔离期，见[缓存](CACHE.md) |
| 导入回执成功但任务未启用 | 仍需用户激活草稿 |
| 多端页面不同步 | SSE 认证、反向代理缓冲、断线游标及日志保留期 |

关闭本地服务使用相同 Compose 项目和环境文件执行 `down`，保留数据卷。部署与数据恢复另见[部署说明](DEPLOYMENT.md)。
