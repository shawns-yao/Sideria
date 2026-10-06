# Sideria · 巡星

单用户、多 Linux 主机的运维工作台：Go 中心与 Agent、Vue 3 / TypeScript、PostgreSQL、Redis。

**当前是可运行的 MVP 实现分支，尚未完成真实双服务器和真实模型验收，不作为生产就绪声明。** [功能清单与验证记录](docs/implementation.md)区分实现、隔离验证和未执行验收；[原始设计](docs/README.md)及 [11 张界面参考图](image/)保留。

## 构建

需要 Go 1.26+、Node.js 22.12+、npm、Linux。依赖由 `go.sum` 和 `web/package-lock.json` 固定。

```sh
make build
make check
make test
```

产物为 `bin/sideria-server` 和 `bin/sideria-agent`；前端嵌入中心，运行时不需要 Node。仅构建 Go 前先运行 `npm --prefix web ci && npm --prefix web run build`。

## 隔离开发与测试

```sh
docker compose -f compose.test.yaml up -d --wait
export SIDERIA_TEST_DATABASE='postgres://postgres@127.0.0.1:55432/sideria_test?sslmode=disable'
export SIDERIA_TEST_REDIS='redis://127.0.0.1:56379/0'
go test -race ./internal/... ./cmd/... ./web
# 可选：仅授权隔离 Docker daemon，测试会创建并删除自己的 Alpine 容器。
docker pull alpine:3.22
export SIDERIA_TEST_DOCKER_SOCKET=/var/run/docker.sock
node scripts/fixture.mjs
```

fixture 启动 loopback 中心和两个真实本地 Agent 进程、临时工作目录/仓库、可选测试容器，临时管理员令牌保存在 `.run/e2e.json`（0600、不提交）。测试不会连接真实服务器或调用外部模型。另一个终端运行：

```sh
cd web
SIDERIA_E2E_URL=http://127.0.0.1:18080 npm run test:e2e
```

浏览器使用 `CHROMIUM_PATH`，缺省 `/usr/bin/chromium`。也可配置为 Playwright 安装的 Chromium 路径。退出 fixture 会终止子进程、清除临时文件和测试容器。测试数据依赖清理：`docker compose -f compose.test.yaml down -v`。

UI 独立预览：`npm --prefix web run dev`，打开 `http://127.0.0.1:5173/?demo=1`。演示无业务 API 请求，不模拟 AI 或终端成功。

## 备份恢复与容量验证

[操作手册](docs/operations.md)提供 PostgreSQL custom archive、恢复到新空库的身份/任务隔离流程，以及 8 个本地 Agent 的有界混合负载测试。恢复验证和负载回归已加入 CI；不等同于生产容量或真实主机验收。逐 Agent 传输带宽、大小和并发见环境模板。

## 接入自有主机（需用户自行授权与配置）

1. 按 [中心环境模板](deploy/server.env.example)注入数据库、Redis、管理员令牌及 TLS；管理员令牌至少 32 字符，不写入 Git。
2. 启动中心，登录后在“设置”创建一次性接入令牌。
3. 在受管 Linux 主机按 [Agent 模板](deploy/agent.env.example)配置中心地址、授权文件根和令牌，启动 Agent。长期身份和执行 journal 仅存本机受限状态目录。
4. [systemd 模板](deploy/sideria-agent.service)提供低权限起点，路径和可写范围必须匹配实际授权。终端和 Docker 默认关闭；不要为了连接成功扩大权限。

开发模式 `SIDERIA_DEV=1` 仅允许 loopback 明文连接。非开发模式要求 TLS，不能通过关闭证书验证连接真实主机。不提供自动安装到真实机器或创建持久凭据的脚本。

## 功能与边界

- 多主机注册、凭据撤销、主动 WebSocket、指标与有界短窗口。
- 授权根内文件浏览/预览，32 MiB 上限的独立传输，新增上传不覆盖现有文件。
- xterm/PTY 多会话；关闭连接终止会话，不自动重放输入。
- Docker 列表/详情/资源/近期日志及启停重启任务；拒绝 Kubernetes 标记资源。
- 项目登记和本地 Git 状态；不 fetch、不修改全局 Git 配置。
- 持久 Task/Step/Execution/Attempt、幂等冲突拒绝、Agent journal 和不确定状态。
- Responses API Observe/Suggest：流式完成事件、函数调用往返、范围/调用预算/脱敏/证据保存。默认禁用外发，无真实模型配置时明确不可用。

没有 AI Execute、Git 写任务、Kubernetes、Compose 部署、完整告警或配置版本平台。上传/download 临时文件、分析与审计保留策略见实现文档；真实部署前需要容量与备份恢复验收。
