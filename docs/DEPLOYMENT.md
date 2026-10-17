# StudyFlow 部署与维护

核对日期：2026-10-17。开发启动见[开发指南](DEVELOPMENT.md)，自动发布见[CI/CD](ci-cd.md)。

## 两种运行方式

| 场景 | 配置与入口 |
| --- | --- |
| 独立运行 StudyFlow | 本仓库 Compose，认证服务另行启动 |
| 两项目联合运行 | JobPilot 部署目录统一维护 Caddy、两个应用、认证和数据库 |

StudyFlow Go 镜像包含构建后的前端及 migrations。MySQL 保存业务数据，Redis 可丢失；认证数据和签名密钥由独立 shared-auth 管理。

## 本机独立 Compose

按开发指南创建一致的 `.env.compose`；先准备 shared-auth，并允许实际浏览器 Origin。然后在仓库根目录：

~~~powershell
docker compose --env-file .env.compose config --quiet
docker compose --env-file .env.compose up -d --build --wait
docker compose --env-file .env.compose ps
~~~

访问 `http://127.0.0.1:8081`。默认构建写入本地 API/认证地址，只适用于相应本机入口。容器默认通过 `host.docker.internal` 查找宿主认证，非 Docker Desktop 环境需显式设置可达地址。

配置变更影响前端地址时需要重新构建，因为 `VITE_API_URL` 和 `VITE_AUTH_URL` 是构建期变量。MySQL 和 Redis 在本机 Compose 发布端口，不能直接把这个开发配置当作已配置反向代理的公网拓扑。

## 联合部署

JobPilot 的[联合部署手册](https://github.com/acid-solution/jobpilot/blob/main/deploy/JOINT_DEPLOYMENT.md)维护三个服务之间的地址、受众、密钥与 Caddy。StudyFlow 构建将两个 Vite 地址设为空，使用同源 `/api`、`/auth`；SSE 与 MCP 同样通过网站入口访问。

`STUDYFLOW_MCP_RESOURCE` 必须与认证及 JobPilot 相同。共享账号仍分别登录，代办令牌只用于受限路线导入。不要同时启动单独栈占用联合栈端口或误用另一份数据卷。

## 数据与备份

持久化对象包括 MySQL 业务库、认证库、认证签名密钥及 JobPilot 资料加密密钥；Redis 不承担恢复业务数据的责任。

发布脚本只备份对应应用业务库，不自动完成全部认证与密钥备份。单独启动也不自动建立周期备份。实际恢复先在隔离库验证 schema、数据和应用兼容，再决定切换窗口，不能只执行 Down 或删除卷。

应用启动执行 Goose 迁移，部署前应保留备份；多个实例的升级顺序要协调，不能只凭版本表认定没有并发风险。

## 上线验收

- 首页、静态资源、API 与数据库健康。
- 正常登录、刷新与两账号资源隔离。
- 创建目标、计划草稿、激活、计时和周报。
- 路线导入回执与手动激活。
- 同账号两页面同步，未保存表单保持原输入。
- SSE 代理未缓冲，断线能补发或重置。

具体范围和方法见[验证指南](VALIDATION.md)。停止服务保留命名卷，正常维护不使用 `down -v`。
