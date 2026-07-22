# Go Todo

基于 Go、Gin、GORM、MySQL、Redis 和 JWT 实现的 Todo 后端学习项目，包含用户鉴权、Todo 管理、分页筛选、Redis 缓存、结构化日志和 Docker Compose 部署。

## 功能

* 用户注册、登录、刷新令牌和退出登录
* JWT 与登录 Session 鉴权
* Todo 创建、查询、更新、完成和删除
* Todo 分页及完成状态筛选
* MySQL 数据持久化
* Redis 列表缓存
* 统一响应结构和请求日志
* Docker 多阶段构建
* Docker Compose 启动 API、MySQL 和 Redis

## 技术栈

| 分类  | 技术                    |
| --- | --------------------- |
| 语言  | Go 1.26.4             |
| Web | Gin                   |
| ORM | GORM                  |
| 数据库 | MySQL 8.4             |
| 缓存  | Redis 8               |
| 鉴权  | JWT                   |
| 容器  | Docker、Docker Compose |

## 项目结构

```text
go-todo/
├── compose.yaml
├── Dockerfile
├── .dockerignore
├── .env.compose.example
├── README.md
├── go.mod
├── main.go
├── static/
└── internal/
    ├── cache/
    ├── config/
    ├── database/
    ├── handler/
    ├── middleware/
    ├── model/
    ├── repository/
    ├── response/
    └── service/
```

## Docker Compose 启动

复制环境变量模板。

Linux/macOS：

```bash
cp .env.compose.example .env.compose
```

Windows PowerShell：

```powershell
Copy-Item .env.compose.example .env.compose
```

编辑 `.env.compose`：

```dotenv
MYSQL_ROOT_PASSWORD=change_me
MYSQL_DATABASE=todo_db
MYSQL_USER=todo_app
MYSQL_PASSWORD=change_me

JWT_SECRET=replace_with_at_least_32_characters
```

构建并启动：

```bash
docker compose --env-file .env.compose up --build -d
```

访问：

```text
http://localhost:8081
```

查看状态和日志：

```bash
docker compose --env-file .env.compose ps
docker compose --env-file .env.compose logs -f
```

停止并删除容器：

```bash
docker compose --env-file .env.compose down
```

彻底清空 MySQL 数据：

```bash
docker compose --env-file .env.compose down -v
```

## API

基础地址：

```text
http://localhost:8081/api
```

受保护接口需要请求头：

```http
Authorization: Bearer <access_token>
```

| 方法       | 路径                | 鉴权 | 说明         |
| -------- | ----------------- | -- | ---------- |
| `POST`   | `/register`       | 否  | 注册         |
| `POST`   | `/login`          | 否  | 登录         |
| `POST`   | `/refresh`        | 否  | 刷新令牌       |
| `POST`   | `/logout`         | 是  | 退出登录       |
| `POST`   | `/todos`          | 是  | 创建 Todo    |
| `GET`    | `/todos`          | 是  | 查询 Todo 列表 |
| `PUT`    | `/todos/:id`      | 是  | 更新 Todo    |
| `PATCH`  | `/todos/:id/done` | 是  | 标记完成       |
| `DELETE` | `/todos/:id`      | 是  | 删除 Todo    |

查询 Todo 支持：

```text
GET /api/todos?page=1&page_size=10&completed=false
```

统一响应格式：

```json
{
  "code": 0,
  "message": "success",
  "data": {}
}
```
