# 事件通知与多端同步

核对日期：2026-10-17。通知让同一账号的不同页面更新展示；它不是带已读状态的消息中心，也不是任务自动完成机制。

## 同事务记录

目标、计划、版本、阶段、任务、计时、导入和偏好变化均通过共用 Service 产生通知。每个完整业务动作在业务事务内记录一条合并事件；任务完成同时结束计时只生成一条，幂等重放不重复生成。

`notification_streams` 保存用户序号及清理边界，`notification_outbox` 保存事件类型、资源、刷新主题及 UTC 时间。事务锁住用户流后分配序号，持续到提交，避免大序号先可见导致补发漏事件。

序号以十进制字符串传输，浏览器使用 BigInt 比较。

## 投递与保留

每个 API 实例每秒查询自己在线订阅用户的 MySQL 日志，按序发送。发送前使本地聚合缓存失效；失败按缓存隔离规则回源。多实例各自服务自己的连接，不依赖 Redis Pub/Sub 或全局已投递标记。

投递为至少一次。网络写成功不等于页面已确认，不因此删除事件；客户端使用游标去重。

默认保留 7 天，每分钟按连续事件前缀清理并同步推进边界，不删除业务执行记录、改期和回执。超出保留范围的客户端恢复最新状态，不承诺收到全部历史操作。

## SSE 协议

`GET /api/v1/events` 使用普通 StudyFlow Bearer Token，不接受指定订阅用户、JobPilot 普通令牌或路线代办令牌。

首次连接收到 `sync.reset` 后刷新。重连通过 `Last-Event-ID` 带上本标签页最后收到的序号；过期或超前游标同样 reset。服务端先订阅，再补发连接时已有事件，发送 `sync.ready` 后继续实时投递。

~~~text
id: 12
event: business.changed
data: {"id":"12","kind":"task.updated","resources":[{"type":"task","id":"任务UUID"}],"topics":["tasks","dashboard","reviews"],"occurred_at":"2026-10-14T00:00:00Z"}

id: 12
event: sync.reset
data: {"cursor":"12","reason":"expired"}

event: sync.ready
data: {"cursor":"12"}
~~~

`sync.ready` 不推进接收游标。每 15 秒发送注释心跳。令牌到期、客户端断开、服务关闭或慢客户端队列满时结束连接，之后可以重连补发。

## 浏览器恢复与编辑

每标签页通过 fetch 流维护一条连接，令牌只在 Authorization 头。游标按 API 地址和账号隔离存入 sessionStorage，退出或换账号时清理。

通知先纳入刷新队列，再保存游标；300ms 内合并主题。查询失败保留展示并重试，确定的客户端错误不循环重试。游标表示已收到并纳入处理，不等于网络查询已成功。

断线以 1–30 秒带抖动退避重连。刷新令牌在标签页内合并，支持 Web Locks 时协调同源标签页。

任务或目标有未保存输入时保留原快照和版本号，提示核对；不能换成最新版号后静默覆盖。版本切换、任务删除或计划结束后保留输入并阻止非法修改。创建表单和导入文字不因列表刷新被清空。

## 服务与代理

`NOTIFICATION_POLL_INTERVAL` 默认 `1s`，`NOTIFICATION_RETENTION` 默认 `168h`。服务退出先关闭订阅再优雅关闭 HTTP；应用没有全局写超时，单次流写入有 5 秒期限。

SSE 代理需关闭缓冲，空闲期限大于心跳周期。Nginx 示例：

~~~nginx
location /api/v1/events {
    proxy_pass http://studyflow_api;
    proxy_http_version 1.1;
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 60s;
}
~~~

应用返回 `text/event-stream`、`Cache-Control: no-cache, no-transform`、`X-Accel-Buffering: no`，跨域允许 `Last-Event-ID`。联合 Caddy 配置见[部署说明](DEPLOYMENT.md)。

实现：[Hub](../internal/studyflow/notification_hub.go)、[Repository](../internal/studyflow/notification_repository.go)、[前端同步](../frontend/src/sync.tsx)。验证见[指南](VALIDATION.md)。
