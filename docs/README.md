# StudyFlow 文档导航

核对日期：2026-10-17。项目入口为 [README](../README.md)，术语以[业务词典](../GLOSSARY.md)为准。

## 按目的阅读

| 目的 | 文档 |
| --- | --- |
| 第一次使用 | [产品说明](PRODUCT.md)、[手动规划](manual-planning.md) |
| 理解模型与不变量 | [领域规则](../STUDYFLOW_V2_PLAN.md) |
| 理解后端分层和系统边界 | [架构说明](../ARCHITECTURE.md) |
| 本地启动、配置和排错 | [开发指南](DEVELOPMENT.md) |
| 接入 HTTP | [API 说明](API.md) |
| 接入 Agent | [MCP 工具](MCP.md) |
| 从 JobPilot 导入路线 | [学习路线导入](learning-route-import.md) |
| Redis 失效与故障恢复 | [缓存说明](CACHE.md) |
| 页面跨设备同步 | [通知说明](notifications.md) |
| 部署、升级、恢复 | [部署说明](DEPLOYMENT.md)、[CI/CD](ci-cd.md) |
| 测试方法和验收范围 | [验证指南](VALIDATION.md) |

## 文档职责

- README 提供产品概览、快速启动及导航。
- 产品文档写用户能做什么，领域规则写状态与约束，词典只定义术语。
- API、MCP、缓存与通知各自维护自己的协议，不在每篇复制。
- 源码中的请求类型与路由是接口核对依据；改接口时一起更新对应说明。
- 验证记录区分单元、真实数据库、前端组件及实际部署，不用某一层通过代替另一层。
- 日期型安排遵循用户时区；具体时刻、存储和服务配置在开发与架构文档说明。
- 旧需求不继续混入现行说明。历史设计可由 Git 历史追溯，原始性能样本保留测量范围。
