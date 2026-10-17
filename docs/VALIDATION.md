# StudyFlow 验证指南

核对日期：2026-10-17，源码基线 `3c3547e`。本文说明验证方法与证据范围；文档重写不把历史通过算作本次重新通过。

## 验证层次

| 层次 | 验证什么 | 前置 |
| --- | --- | --- |
| 普通 Go 测试 | 领域计算、缓存端口、认证等单元行为 | Go 及依赖 |
| MySQL 集成 | 事务、幂等、版本、隔离、通知与约束 | 一次性测试库 |
| 迁移回滚 | 空库升级、回滚与重建 | 另一个独立空库 |
| Redis 集成 | Redis 协议和恢复行为 | 独立测试 Redis |
| 前端组件 | 创建流程、同步与未保存输入保护 | Node 与前端依赖 |
| 实际业务验收 | 注册、两类计划、导入、计时、跨页同步 | 隔离账号和实际栈 |

## 普通检查

仓库根目录：

~~~powershell
go test ./...
go vet ./...
~~~

前端：

~~~powershell
Set-Location frontend
npm ci
npm test
npm run typecheck
npm run build
~~~

缺少测试数据库时，相关 Go 集成测试会显示跳过。只看命令退出码不能宣称数据库事务已验证。

## 隔离数据库

`TEST_MYSQL_DSN` 指向独立可清理的业务测试库，使用 `parseTime=True`、`loc=UTC`、`multiStatements=true`。`TEST_MIGRATION_DSN` 必须指向另一个空库，迁移测试包含 Down，不能与普通集成库或开发数据共用。

配置测试进程后分别运行：

~~~powershell
go test -count=1 -v ./internal/studyflow
go test -count=1 -v ./internal/database -run '^TestMigrationsFromEmptyAndRollback$'
~~~

Redis 集成使用 `TEST_REDIS_ADDR`，其余参数以[Redis 测试](../internal/cache/redis_test.go)为准。Linux CI 使用 `-race`；本机若缺对应工具链，不把未运行 race 写成通过。

## 关键回归

- 两个用户访问相同资源 ID，越权返回 404。
- 一个目标多计划，一个计划独立版本，复制后的来源与副本互不影响。
- 乐观锁并发改期，单用户唯一运行计时。
- 重复导入重放、同键异载荷冲突、部分写入失败整批回滚。
- 路线只更新变化计划，已有草稿冲突，代办摘要和权限拒绝。
- 通知与业务同事务、并发序号、重复补发与过期游标。
- Redis 失效未确认、重启隔离、迟到回填。
- 手动创建与放弃默认、跨客户端修改保留输入。

对应测试位于 [internal/studyflow](../internal/studyflow)、[internal/database](../internal/database)、[frontend/src](../frontend/src)。以测试名、运行日志和版本记录实际结果，不固定承诺测试数量。

## CI 与部署

[CI/CD](ci-cd.md)把普通数据库和迁移回滚分在独立 job，前端执行测试、类型检查与构建。镜像与健康检查不等于完整导入或浏览器验收，部署后仍按[产品流程](PRODUCT.md)核对。

旧缓存耗时仅为[历史本机样本](CACHE.md#历史性能样本)。本轮没有新测吞吐、模型质量或线上运行状态。

## 文档检查

修改文档后检查本地链接、标题锚点、代码围栏、JSON 示例、配置名称与源码路径，并运行 `git diff --check`。协作规则、真实凭据与本机配置不纳入文档提交。

### 2026-10-17 文档重整检查

两仓合计检查 44 份业务与工程文档、285 个本地链接及锚点、JSON 示例、23 段 PowerShell 和 11 段 Bash 语法；共 7 组 Compose 配置通过静态解析，其中包括本仓库的独立 Compose。接口方法、配置名和源码入口按当前实现核对。

本轮未启动服务、改动业务数据、调用真实模型或重新运行数据库/浏览器功能验收；这些结果与文档静态检查分别记录。
