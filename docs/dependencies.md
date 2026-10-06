# 依赖与模块选择

版本以 go.mod/go.sum 与 web/package-lock.json 为准；以下为 2026-10-06 实施验证时的直接依赖。

| 模块 | 版本 | 职责 |
| --- | --- | --- |
| gorilla/websocket | 1.5.3 | RFC WebSocket 与帧处理，应用层另加认证/限额 |
| pgx/v5 | 5.11.0 | PostgreSQL 连接池、事务、参数绑定 |
| go-redis/v9 | 9.23.0 | Redis 原子限流及 owner 租约 |
| gopsutil/v4 | 4.26.9 | Linux CPU、内存、磁盘、网卡与系统采集 |
| creack/pty | 1.1.24 | PTY 创建和窗口调整 |
| bbolt | 1.5.0 | Agent 本地 fsync 执行 journal |
| Vue | 3.5.43 | 组件与响应式 UI（MIT） |
| @lucide/vue | 1.52.0 | 一致线性图标（ISC） |
| @xterm/xterm / addon-fit | 6.0.0 / 0.11.0 | 真实终端显示与 resize（MIT） |
| zod | 4.6.5 | API 指标运行时校验（MIT） |

Go 直接依赖使用各上游仓库随包提供的许可文本；分发时保留许可，不变更上游授权。项目自身尚未新增开源许可证，不能凭依赖许可证替用户决定项目授权。

官方依据：[Go os.Root](https://pkg.go.dev/os#Root)、[Gorilla](https://github.com/gorilla/websocket)、[pgx](https://github.com/jackc/pgx)、[go-redis](https://redis.io/docs/latest/develop/clients/go/)、[Docker Engine API](https://docs.docker.com/reference/api/engine/)、[Responses function calling](https://developers.openai.com/api/docs/guides/function-calling)、[Responses streaming](https://developers.openai.com/api/docs/guides/streaming-responses)。

Docker 和 Responses 各保留一个窄 HTTP 适配边界，未引入通用工作流/Agent 框架：只覆盖本项目实际开放的 API，保留契约测试；如果范围扩展，优先评估官方 SDK 迁移，避免演变为自建通用 SDK。工具授权与任务恢复属于 Sideria 自身策略，不能由 SDK 替代。

UI 数据、展示与 API 客户端分离；终端按需加载。中心为单体但将存储、连接、任务、身份、传输、AI 和 HTTP 处理拆为模块，Agent 将 journal、采样、文件/Git、Docker、PTY 拆分。未预先引入微服务、消息队列或多租户。
