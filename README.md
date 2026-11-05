# new-api-invoice

New API 独立发票工作台：一个 Go 进程、一个 Docker 镜像提供 React 页面、业务 API 和私有附件下载。连接**同一个 New API PostgreSQL 数据库**，无需另建业务库或额外 Redis，未修改宿主代码。

已实现选单合并申请、企业抬头、四状态历史、下载；后台支持用户检索、逐用户授权/撤销、代办、代建抬头、驳回、上传完成和操作记录。撤销后保留历史访问及待处理申请，代办不会自动开通权限。

Invoice v0.1。提供完整前后端、数据库迁移、自动建表、容器构建及本地测试。真实部署前应验证原站认证兼容、旧权限与附件接管；测试不代表财务或业务验收。

## 接入边界

- Go 1.26.10 / Gin / pgx / decimal，React 19 / TypeScript / Rsbuild / TanStack / Bun 1.3.14；支持 PG17/18。
- 只读 users、top_ups 必需列，复用旧发票三表，新增权限/审计/幂等/迁移/初始化五表。
- 只接受 New API 面板 access JWT，每次原样回源 `/api/user/self` 后核对数据库当前身份；不复制密码或签名密钥，PAT、模型 Key、security_proof 不支持。
- 同源代理 `/invoice/` 与 `/invoice-api/v1/` 到本服务，原站认证 `/api/user/auth/*`、`/sign-in` 继续进入原站。[代理示例](docs/reverse-proxy.example.conf)。原站配置 SESSION_COOKIE_SECURE=true、TRUSTED_URL 为外部 HTTPS origin。
- 登录在新标签页打开，完成后返回点击“我已完成登录，继续”。令牌只在内存，刷新锁/会话广播兼容原站；写请求不在登录后自动重放。
- 公共充值表没有币种列，必须核对 PAYMENT_CURRENCY_RULES；未知或非 CNY 拒绝开票。外部退款不具备跨服务原子事务。

## 配置与构建

复制 [.env.example](.env.example) 为 `.env` 并填写实际配置。Compose 读取 env_file；直接运行 Go 程序需注入进程环境，程序不会自动加载 .env。

必填 DATABASE_URL（原站同库专用运行角色）、NEW_API_INTERNAL_URL（固定 HTTP(S) origin）、PUBLIC_ORIGIN（精确 HTTPS origin，仅 loopback 可 HTTP）、PAYMENT_CURRENCY_RULES（非空 JSON 数组）。例如经确认后可用 `[{"provider":"epay","method":"alipay","currency":"CNY"}]`，不代表当前订单已确认。

可选 INVOICE_FILE_ROOT 默认 `/data/invoices`、LISTEN_ADDR 默认 `:3001`、AUTH_TIMEOUT 默认 `3s`、READ_ONLY_MODE 默认 `false`。维护时只读模式拒绝全部业务写请求。

```powershell
cd web
bun install --frozen-lockfile
bun run build
cd ..
go build -o output/invoice.exe ./cmd/invoice
docker build -t invoice:v0.1-local .
```

## 首次启动与旧库迁移

默认 `AUTO_MIGRATE=true`。新版New API完全没有发票表时，直接启动服务即可自动创建8张发票表，并在同一事务记录新安装标记；不要求users.invoice_enabled或旧options配置。所有用户默认未开通，管理员仍按New API角色进入后台授权。

数据库账号必须有首次建表权限。最小权限部署可另配 `MIGRATION_DATABASE_URL`（与DATABASE_URL相同host/port/database的迁移账号）：仅需要初始化时连接，在同一事务创建表并为运行角色授予发票表业务权限、宿主必需列只读权限，完成后关闭连接。已就绪的库不连接迁移账号。部分缺表、结构/数据异常不会自动重建修复。建表或授权失败整笔回滚，修正配置后可重试。

`AUTO_MIGRATE=false`可保留显式迁移模式。对于已有旧发票数据但未继承权限的库，备份数据库和附件、停止旧写入口后，仍显式执行以下命令，避免自动猜测历史授权来源：

```powershell
./output/invoice.exe migrate
./output/invoice.exe preflight
./output/invoice.exe init-permissions --dry-run
./output/invoice.exe init-permissions
```

已有旧表只校验，不重建、修复或重置序列；缺旧表时要求兼容宿主基线已存在，再创建等价三表。事务/应用锁/checksum 保护迁移。不兼容数据需另行受控处理。

初始化严格读取 options 中 SidebarModulesAdmin.personal.invoice 和 users.invoice_enabled；来源缺失/坏 JSON 会停止。仅在人工确认旧全局值后，可附 `--legacy-global=true` 或 `--legacy-global=false`，先 dry-run 再执行。固定快照不覆盖已有人工权限，重复初始化不会补开后来用户；服务需要迁移/初始化完成标记。

使用 [runtime-grants.sql](docs/runtime-grants.sql) 按 psql 的 runtime_role 变量给专用角色授权，换回运行账号后启动：

```powershell
./output/invoice.exe serve
# 或准备 .env 后单服务部署
docker compose -f compose.example.yaml up -d
```

Compose 只包含 invoice；原站和数据库需能从容器访问，容器名访问需共享 Docker 网络。根文件系统可只读，UID/GID 10001，附件卷必须可写。不继承宿主公共表写权限，审计只授予 INSERT/SELECT。

`/healthz` 存活，`/readyz` 检查数据库、迁移 checksum、初始化标记和附件目录。认证成功后每账号每分钟 240 次；匿名失败有独立网络桶，回源最多同时 32 个，不信任任意 XFF。

## 附件与回退

单文件 PDF/JPG/JPEG/PNG <=10MiB，总请求 <=11MiB；校验扩展/MIME/签名及图片解码/像素。私有目录不暴露静态 URL，下载必须 Bearer。文件先落盘再提交；提交结果未知保留文件并按原请求键恢复，不能自动换键。

```powershell
./output/invoice.exe reconcile-files
```

该命令只报告未引用、缺失或不安全路径，不删除。历史绝对路径/越界/符号链接须受控转换并接管实际文件；缺文件返回 410，不改已开票状态。签名校验不代表税务或恶意内容认证。

回退时只读或停止服务，保留表、幂等、审计和附件，不用 DROP/清空回滚；恢复旧程序需另核对权限和状态语义。

## 本地验证

```powershell
./scripts/verify-local.ps1 -DatabaseMatrix -DockerBuild
```

先准备专用 PG17/18 容器（**仅本地虚构测试凭据**）：

```powershell
docker run -d --name invoice-v01-test-pg17 -p 127.0.0.1:55417:5432 -e POSTGRES_PASSWORD=invoice_test_only -e POSTGRES_DB=invoice_test postgres:17-alpine
docker run -d --name invoice-v01-test-pg18 -p 127.0.0.1:55418:5432 -e POSTGRES_PASSWORD=invoice_test_only -e POSTGRES_DB=invoice_test postgres:18-alpine
```

容器已存在时使用 `docker start invoice-v01-test-pg17 invoice-v01-test-pg18`。

测试只允许 loopback/host.docker.internal 的 invoice_test，会重置专用库 schema；绝不能指向现有 New API 库。无 INVOICE_TEST_DATABASE_URL 会明确 skip 集成。Linux/CGO 另运行 `go test -race -count=1 ./internal/...`。scripts/browser-host.py、browser-fixture.sql 是纯本地认证边界桩，不是生产依赖或真实原站联调。

代码入口 cmd/invoice；auth 适配、store 迁移/继承、billing 发票用例和事务、server HTTP、files 私有文件、money 精确金额位于 internal；前端 web/src 构建后 embed。没有宿主模块或临时旧项目构建依赖。

## 项目结构与验证

- `cmd/invoice/`：启动与迁移、预检、权限继承、附件核对命令。
- `internal/auth/`：原站身份回源；`store/`：自动建表、迁移和旧授权继承。
- `internal/billing/`：发票用例、并发占用、幂等及审计；`server/`：24个业务接口。
- `internal/files/`：私有文件；`money/`：精确金额；`web/src/`：React工作区。
- `docs/`：反向代理与最小权限授权脚本。

接口前缀为 `/invoice-api/v1`，覆盖当前身份/写结果恢复、抬头、订单/申请/文件、后台用户/权限和操作记录。所有业务请求使用原站面板Bearer JWT；写入另需同源Origin及UUID Idempotency-Key。ID、分值和版本使用十进制字符串；申请状态为pending/invoiced/rejected/cancelled。

本地已验证PG17/18全新建表、旧库迁移、并发占用、权限撤销、状态竞争、幂等、审计回滚、提交结果未知恢复、私有文件，以及前端类型/lint/测试/build和Linux race。跨操作系统、真实代理/登录方式及历史数据仍应按自身环境验证。

自动建表默认开启。可选 `MIGRATION_DATABASE_URL` 为同host/port/database的迁移账号，仅启动未就绪时连接，建表/标记/运行角色授权同事务完成后关闭；未提供时由DATABASE_URL账号执行首次建表。需要迁移时不允许跨端点fallback。已有ready库不打开迁移连接，也不重复继承或覆盖权限。
