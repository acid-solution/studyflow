# JobPilot 学习路线导入

核对日期：2026-10-17。来源路线由 JobPilot 生成与确认，StudyFlow 原子保存草稿，不负责生成任务或自动激活。

## 首次与后续导入

首次建立一个求职用途目标、项目实践和知识准备两份计划及草稿版本。后续相同来源路线只为实际改变的计划新增草稿，不覆盖任务、计时和改期。

来源 `source_version` 与计划 `version_no` 分别保存。只更新知识时，项目版本对应关系仍保留；已有草稿返回冲突，用户需先激活或取消。回执成功说明已导入，不说明已启用。

## 输入与摘要

MCP `import_learning_route` 参数为 `idempotency_key` 与 `payload`。载荷包含 `source_route_id`、`source_version`、目标名称与说明、`weekly_total_minutes` 及 `plans`。每项带 `role`（`project` / `knowledge`）和计划内容，首次必须两项齐全。

每份计划最多 50 阶段、200 任务；课时任务不能带日期，总周预算不能超过声明投入。具体类型见[learning_route.go](../internal/studyflow/learning_route.go)。

摘要使用 JSON 解码后按键排序编码的 SHA-256，不包含传输幂等键。相同键与相同内容重放原回执，不同内容冲突。回执和整次领域写入同事务，不留下半份路线。

## 受限代办令牌

`STUDYFLOW_MCP_RESOURCE` 与 shared-auth 的同名配置及 JobPilot 的 MCP 地址完全一致；留空拒绝代办令牌。

代办令牌绑定当前用户、资源、导入键和内容摘要，只能导入固定路线或查询匹配回执，不能访问普通 API 或其他 MCP 工具。普通 StudyFlow 登录令牌仍按原受众验证。

权限与摘要由服务端校验，客户端不能靠修改 `user_id` 改变归属。详见[认证与 MCP](MCP.md)。

## 响应不明与页面定位

调用超时后先通过 `get_learning_route_import` 用同一键核实，未知不能当作未创建后换键重发。

回执中的目标、计划、版本可组成 `/?goal_id=...&plan_id=...&version_id=...`。打开后正常登录并读取当前用户有权访问的版本，再主动激活草稿。页面完成不升级能力，计划之间的前置说明不形成完成锁。

实现：[路线服务](../internal/studyflow/learning_route.go)、[MCP 权限](../internal/studyflow/learning_route_mcp.go)、[回执与绑定](../internal/studyflow/learning_route_repository.go)。验证见[指南](VALIDATION.md)。
