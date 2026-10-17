# StudyFlow MCP 工具

核对日期：2026-10-17。`/mcp` 使用 MCP Streamable HTTP，与普通 API 同进程同端口；每个请求携带自己的 Bearer Token。工具共用应用服务，不另写一套领域规则。

## 工具清单

| 工具 | 作用 | 关键输入 |
| --- | --- | --- |
| `import_plan_tree` | 事务导入单份计划及 V1 草稿 | `idempotency_key`、目标与计划载荷 |
| `query_progress` | 查询生效计划、当前版本进度与任务 | 对应筛选条件 |
| `reschedule_task` | 修改或清除任务安排日期 | 任务 ID、原 `version`、日期 |
| `import_learning_route` | 导入 JobPilot 两份路线计划或更新 | `idempotency_key`、`payload` |
| `get_learning_route_import` | 核实固定路线的真实回执 | `idempotency_key` |

声明和参数类型以 [mcp.go](../internal/studyflow/mcp.go)、[路线类型](../internal/studyflow/learning_route.go)为准。当前为五个工具，不把历史三个工具的描述当成完整清单。

## 普通身份与代办身份

普通 StudyFlow Access Token 使用同一 RS256/JWKS、issuer、audience 验证，可按账号访问工具。签给 JobPilot 的普通 Token 不能直接复用。

受限路线代办令牌仅能使用后两个工具，并绑定用户、MCP 资源、导入键、内容摘要和权限。`STUDYFLOW_MCP_RESOURCE` 留空时不接收代办；它与认证、JobPilot 配置必须完全一致。普通 API 和 SSE 不接受这种令牌。

## 幂等与并发

MCP 幂等键位于工具参数，不依赖传输头。请求超时后继续原键核实或重试；同键换内容冲突。HTTP 单计划树导入的规范化、数量限制和失败回滚同样适用。

`reschedule_task` 必须使用查询取得的任务版本号；不在工具内偷偷读取最新版本来覆盖旧修改。日期任务可清除日期，课时任务遵守对应模式约束。

工具返回真实事务结果，不根据模型文字生成成功回执。错误不授权重建另一份相同计划。

## 用户确认边界

调用方负责在写入前向用户展示固定内容并取得确认；服务端仍核验身份、状态、摘要和约束。MCP 接入不会自动激活版本、完成任务或提升能力。

JobPilot 使用受限代办，仅导入与核实固定路线，不获得任意操作权限。网页查看草稿仍正常登录。路线的独立版本与增量导入见[专项说明](learning-route-import.md)。

## 验证

[测试指南](VALIDATION.md)区分工具调用协议、真实 MySQL 原子性、代办拒绝和浏览器操作。测试客户端也必须提供合法测试身份，不能用关闭鉴权代替验证。
