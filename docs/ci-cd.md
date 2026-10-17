# StudyFlow CI/CD

核对日期：2026-10-17。精确任务以[工作流](../.github/workflows/ci.yml)为准。

## 检查与构建

push、PR 和手动触发均检查：

1. 临时 MySQL 普通集成库，`TEST_MYSQL_DSN` 配合 Go race 和 vet。
2. 独立 migrations job，在全新 `studyflow_migration_test` 用 `TEST_MIGRATION_DSN` 验证空库升级及回滚，不与普通库混用。
3. 前端 `npm ci`、测试、类型检查和生产构建。
4. 构建 linux/amd64 镜像；前端 API/认证地址留空，使用同源入口。

只有受信任 main 发布 `ghcr.io/acid-solution/studyflow:<commit>`，摘要记录固定 digest。PR 不发布、不获取部署 SSH 凭据；Action 固定提交。

## 发布配置

两个应用共享服务器部署目录与文件锁。StudyFlow 仓库配置 `DEPLOY_SSH_KEY`、`DEPLOY_KNOWN_HOSTS` Secrets，以及 `DEPLOY_HOST`、`DEPLOY_PORT`、`DEPLOY_USER`、`DEPLOY_PATH`、`DEPLOY_ENABLED` Variables。

初次安装、配置、联合栈与业务验收完成后才启用发布。是否已启用是具体环境的状态，不能从模板推定。Verify SSH 仅核验连接，不替代上线。

## 服务器更新

工作流调用 JobPilot 部署目录的 `release.sh studyflow <固定digest>`，拉取已验证镜像、备份 MySQL、替换 StudyFlow 并做健康/首页检查。不会重建认证、JobPilot 或数据库。

两个仓库使用同一服务器锁串行发布，不只依赖各自 GitHub concurrency。完整初始化、镜像权限、HTTP 联调标记、失败日志和恢复方法统一维护在[发布手册](https://github.com/acid-solution/jobpilot/blob/main/deploy/CICD.md)。

## 失败与恢复

发布失败保留旧清单、备份与日志，不代表旧容器已恢复。新应用可能已执行迁移，先核对旧版本兼容性再回退镜像；不自动降级数据库。

CI、镜像构建、服务健康和实际业务分别验证，方法见[验证指南](VALIDATION.md)，本机与联合部署见[部署说明](DEPLOYMENT.md)。
