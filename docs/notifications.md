# 事件通知与多端同步

通知用于让同一账号的页面更新展示，不是有已读状态的消息中心。目标、计划、计划版本、阶段、任务、计时、导入和用户偏好均产生通知，REST 与 MCP 共用同一套 Service。

## 可靠记录与顺序

每个完整业务操作在原业务事务内记录一条合并通知。`notification_streams` 保存每个用户的最新序号和清理边界，`notification_outbox` 保存事件类型、资源标识、刷新主题及 UTC 发生时间。业务提交和事件记录一起成功或一起回滚；完成任务并结束计时只生成一条通知，幂等重放不重复通知。

序号由事务锁住用户通知流后分配，锁持续到提交。这样同一用户的较大序号不能提前于较小序号变成可见数据，补发查询不会因为并发提交漏掉事件。序号以十进制字符串传输，浏览器使用 BigInt 比较，不依赖 JavaScript 浮点数精度。

后台每秒检查本实例的在线订阅用户，按序读取 MySQL 日志。通知推送前使本地读缓存失效，Redis 失败则按现有隔离策略回源。多个 API 实例可以独立从同一 MySQL 投递给各自连接，不需要消息队列或 Redis Pub/Sub，也没有全局已读/已投递标记。

每分钟按批次删除超过保留时间的连续事件前缀，并在同一事务里更新清理边界。Outbox 默认保留 7 天；仍未过期的前缀不会为了删除后面的旧时间戳事件而被越过。不会删除业务执行记录、改期记录或导入回执。

## SSE 契约

`GET /api/v1/events` 使用普通 StudyFlow Bearer Access Token。身份由验签结果确定，不接受指定订阅用户，也不接受 JobPilot 或代办令牌。

首次连接接收 `sync.reset`，客户端刷新当前查询。重连在 `Last-Event-ID` 请求头传入本标签页最后已接收的序号；过期或大于服务端序号的游标同样触发 reset。服务端先建立实时订阅，再补发截至连接时最新序号的事件，最后发送 `sync.ready` 并继续实时推送。

```text
id: 12
event: business.changed
data: {"id":"12","kind":"task.updated","resources":[{"type":"task","id":"任务 UUID"}],"topics":["tasks","dashboard","reviews"],"occurred_at":"2026-10-14T00:00:00Z"}

id: 12
event: sync.reset
data: {"cursor":"12","reason":"expired"}

event: sync.ready
data: {"cursor":"12"}

```

同一序号的重复通知不会再次触发刷新。投递采用至少一次语义：断线后可能补发重复事件，由游标过滤；成功写入网络不等于客户端确认，因此不会删除日志。`sync.ready` 不推进接收游标。保留窗口以外恢复最新状态，不承诺重放全部历史操作。

每 15 秒发送注释心跳。令牌到期、客户端断开、应用关闭或慢客户端队列填满时停止连接；重连可重新补发。后台查询失败不会影响业务写入，恢复后继续读取尚未投递的序号。

新增 `GET /api/v1/tasks/:id` 返回包括计划状态、计划版本状态在内的最新任务详情。历史和草稿任务同样可以读取，但继续遵守当前用户的数据归属与修改限制。

## 前端恢复与编辑保护

每个标签页通过 fetch 流维护一条连接，令牌只进入 Authorization 请求头。接收游标放在按 API 地址和账号隔离的 sessionStorage 中，退出或换账号时取消连接并清理旧游标。连接失败以 1 到 30 秒带抖动退避重试；刷新令牌调用在标签页内合并，支持 Web Locks 的浏览器还会协调同源标签页。

接收到的通知先进入刷新队列，再保存接收游标；队列在 300ms 内合并刷新主题。查询失败保留旧展示并重试，404 等确定的客户端错误不循环重试。游标证明通知已被接收和纳入刷新处理，并不代表一次网络查询已经成功。

任务抽屉没有未保存修改时自动加载最新详情和计时。有修改时保留输入与原乐观锁版本号，提示重新加载；不会自动给旧输入换上新版本号。删除任务、结束计划或切换版本后，相关抽屉保留输入并阻止修改。目标编辑同样保留编辑快照；计划创建表单和导入文本不会因为列表刷新被清空。

## 配置与代理

```env
NOTIFICATION_POLL_INTERVAL=1s
NOTIFICATION_RETENTION=168h
```

API 不设置全局 HTTP 写超时；单次流写入有 5 秒写期限。服务退出时先关闭订阅，再完成 HTTP 优雅关闭。

Nginx 代理 SSE 路由时必须禁用缓冲，且空闲读取期限应大于心跳间隔：

```nginx
location /api/v1/events {
    proxy_pass http://studyflow_api;
    proxy_http_version 1.1;
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 60s;
}
```

应用已返回 `Content-Type: text/event-stream`、`Cache-Control: no-cache, no-transform` 和 `X-Accel-Buffering: no`。跨域白名单允许 `Last-Event-ID` 请求头。HTTPS 部署可启用浏览器 Web Locks；不支持时仍提供标签页内刷新合并。
