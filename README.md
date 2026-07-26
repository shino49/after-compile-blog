# After Compile · 编译之后

> **Build systems. Keep thinking.**

After Compile 是一个用于技术分享与个人感悟的单用户博客。项目采用“夜间终端”视觉语言，但仍以清晰、舒适的长文阅读和高效内容管理为核心。

## 功能

- 响应式首页、分类/标签分页、文章详情、目录、Markdown 与代码高亮、归档、全文搜索和关于页
- 单管理员后台：安全登录、统计、筛选、新建、编辑、发布/撤回及删除文章
- HttpOnly Session Cookie、bcrypt 密码、服务端令牌摘要、同源写入检查和完整字段校验
- 草稿严格隔离于公开详情、列表、搜索、精选和归档 API
- SQLite 自动迁移、幂等示例内容和初始管理员创建
- 单容器生产部署，Go 同时提供 API、静态资源和 Vue Router history fallback

## 技术栈

Go 1.24、chi、纯 Go `modernc.org/sqlite`、Vue 3、TypeScript、Vite、Vue Router、Pinia、marked、highlight.js 和 Vitest。

## 目录结构

```text
cmd/server/          Go 服务入口
internal/app/        HTTP、认证、数据访问、迁移与后端测试
web/src/views/       博客前台页面
web/src/admin/       管理后台页面与测试
web/src/components/  通用文章和 Markdown 组件
Dockerfile           Node/Go/Alpine 多阶段镜像
docker-compose.yml   单服务与持久化卷
```

## 本地开发

要求 Go 1.24+ 和 Node.js 22+。

```bash
cp .env.example .env
# 编辑 .env，设置至少 12 位且唯一的 ADMIN_PASSWORD
set -a; source .env; set +a
npm ci --prefix web
make dev-api                 # API：http://localhost:8080
# 另一个终端
make dev-web                 # 前端：http://localhost:5173（代理 API）
```

首次启动会自动建表；仅当数据库没有用户时，才根据 `ADMIN_USERNAME` 和 `ADMIN_PASSWORD` 创建管理员。没有配置凭据时服务仍会启动，但后台无法登录。示例文章只在空文章表中插入，重复启动不会重复生成。

### 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ADMIN_USERNAME` | 无 | 首次初始化管理员用户名 |
| `ADMIN_PASSWORD` | 无 | 首次初始化密码，至少 12 位；不要提交真实值 |
| `DATABASE_PATH` | `./data/blog.db` | SQLite 文件路径 |
| `PORT` | `8080` | 服务端口 |
| `WEB_DIST` | 自动检测 `web/dist` | 前端构建目录 |
| `COOKIE_SECURE` | `false` | HTTPS 生产环境必须设为 `true` |

## 生产构建与运行

```bash
npm ci --prefix web
npm --prefix web run build
CGO_ENABLED=0 go build -o after-compile ./cmd/server
ADMIN_USERNAME=admin ADMIN_PASSWORD='your-long-random-secret' ./after-compile
```

默认前台为 <http://localhost:8080>，后台为 <http://localhost:8080/admin/login>，健康检查为 <http://localhost:8080/api/health>。

## Docker Compose

```bash
cp .env.example .env          # 替换密码
# 若经 HTTPS 反向代理提供服务，把 COOKIE_SECURE 改为 true
docker compose up --build -d
docker compose ps
curl http://localhost:8080/api/health
```

数据库保存在容器 `/data/blog.db`，由 `blog-data` Docker Volume 持久化。应用是唯一容器，不需要 Nginx。生产环境应置于 HTTPS 反向代理后，并启用 `COOKIE_SECURE=true`。

## 测试与检查

```bash
gofmt -w cmd internal
go vet ./...
go test ./...
npm --prefix web test
npm --prefix web run typecheck
npm --prefix web run build
CGO_ENABLED=0 go build ./cmd/server
```

## API 概览

公开端点：`GET /api/posts`、`/api/posts/{slug}`、`/api/featured`、`/api/archive`、`/api/search?q=`。筛选使用 `/api/posts?category=技术&tag=go&page=1`。

认证端点：`POST /api/auth/login`、`POST /api/auth/logout`、`GET /api/auth/me`。管理端点位于 `/api/admin`，包括 `/dashboard` 及 `/posts` CRUD。错误使用统一的 `{ "error": { "code", "message", "fields" } }` 结构。
