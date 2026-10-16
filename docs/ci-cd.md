# CI/CD

仓库 `.github/workflows/ci.yml` 在 push、PR、手动触发时执行：

- 后端在临时 MySQL 中运行 `go test -race -count=1 -v ./...` 和 `go vet ./...`。
- 迁移回滚使用单独 job 和全新的 `studyflow_migration_test` 库，设置 `TEST_MIGRATION_DSN`。不会与普通集成测试共用数据库，也不会因缺少该变量默认跳过。
- 前端运行 `npm ci`、`npm test`、`npm run typecheck` 和 `npm run build`。
- 检查通过后构建 linux/amd64 镜像，前端 API/认证地址留空以使用同源路径。受信任 main 发布 `ghcr.io/acid-solution/studyflow:<commit>`，运行摘要保存固定 digest。PR 只验证构建，不发布、不获取部署凭证。

服务器使用 JobPilot 仓库的联合 Compose 和 `deploy/release.sh`。两个仓库连接同一个部署目录，通过同一个服务器锁串行更新；StudyFlow 发布只更新 StudyFlow，不重建认证和数据库。

仓库设置两个 Secrets：`DEPLOY_SSH_KEY`、`DEPLOY_KNOWN_HOSTS`；设置 Variables：`DEPLOY_HOST`、`DEPLOY_PORT`、`DEPLOY_USER`、`DEPLOY_PATH`、`DEPLOY_ENABLED`。账号为通用部署账号，例如 `acidsolution-deploy`；路径为 `/srv/jobpilot-stack/jobpilot/deploy`。

`DEPLOY_ENABLED` 初始设为 `false`。安装 Docker、配置实际运行环境、确认镜像下载、完成联合栈首次启动及业务验收后，再设为 `true`。部署使用 `production` environment，保留可信主机校验，不接受未知 SSH 主机。

可先手动运行 `Verify SSH` 工作流，核验 GitHub 执行环境能否使用专用密钥登录正确账号；该流程不安装软件或启动网站。

完整首次部署、镜像访问权限、数据库备份与失败恢复边界见 [JobPilot 联合 CI/CD 说明](https://github.com/acid-solution/jobpilot/blob/main/deploy/CICD.md)。回退镜像不能替代数据库恢复，正式开启发布前需演练。
