# JobPilot 学习路线导入

JobPilot 负责规划，StudyFlow 负责执行。MCP 新增 `import_learning_route` 和 `get_learning_route_import`，原有三个工具继续保留。

首次导入原子创建求职目标、项目实践计划与知识准备计划及两份草稿；后续相同来源路线仅为内容改变的计划增加草稿。路线版本与执行版本各自编号。知识单独更新不会替换项目任务、计时或改期记录。若计划已有草稿，先在 StudyFlow 中激活或取消，不自动覆盖。

导入参数由固定 `idempotency_key` 和 `payload` 组成。payload 包含 `source_route_id`、`source_version`、`goal_title`、`goal_description`、`weekly_total_minutes` 和 plans；每项包含 role（project/knowledge）与复用现有导入格式的 plan。首次必须包含两项。每份最多 50 阶段、200 任务。课时任务不能带日期；总周预算不得超出声明的总投入。

内容摘要为 payload 经 JSON 解码后重新排序键编码的 SHA-256；不包含传输幂等键。重复键与相同内容返回原回执，不同内容报冲突。回执包含目标和两份计划版本，即使只更新知识，也保留项目版本对应关系。

`STUDYFLOW_MCP_RESOURCE` 与 shared-auth 的同名配置完全一致，留空不接受代办令牌。普通 StudyFlow 登录令牌沿用原方式；JobPilot 的短期代办令牌只允许学习路线导入和固定摘要的回执查询，不能使用普通 API 或其他 MCP 工具。普通网页仍需登录。

回执中的 ID 可组成 `/?goal_id=...&plan_id=...&version_id=...` 链接；登录后读取受账号权限约束的计划并定位版本。导入不等于激活，不会自动完成任务或升级能力。
