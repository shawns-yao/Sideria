# 备份、恢复与容量验证

本文记录当前实现的操作步骤和实测边界，不代表生产部署或真实双主机验收。

## PostgreSQL 备份

采用官方 [pg_dump](https://www.postgresql.org/docs/17/app-pgdump.html) 的 custom archive；不自建数据库导出格式。Python 3 标准库负责私有目录、SHA-256 清单和调用边界。安装与数据库同主版本的官方 PostgreSQL 客户端；本轮验证版本为 PostgreSQL 17。

连接由 libpq 的 `PGHOST`、`PGPORT`、`PGDATABASE`、`PGUSER`、`PGSSLMODE`、`PGPASSFILE` 等环境设置提供。密码不要放在命令行或 Git。已有凭据如何注入由部署方管理，脚本不会创建凭据。

```sh
# 先配置本机授权的 libpq 环境，目录父级须由操作员控制。
python3 scripts/database-backup.py backup /private/backups/sideria-20261006
```

目标目录必须不存在。生成的目录权限为 0700，归档和 manifest 为 0600；文件 fsync 完成后发布目录。清单含归档大小、SHA-256、备份 ID、数据库名及 PostgreSQL 版本，不含连接密码。SHA-256 用于检测损坏，不提供来源真实性认证。

`pg_dump` 保证数据库内部快照一致；不等于数据库与 Agent 外部副作用同时冻结。需要跨系统一致检查点时，先暂停新任务并等待已运行操作结束，再停止中心，记录 Agent journal 的保管位置。不要为了备份删除或重建 Agent journal。

备份包含数据库内的主机、项目、任务、Attempt、审计、分析及最新指标快照。它**不包含**角色/表空间、部署环境配置、TLS 私钥、模型密钥、Agent 本机 journal、内存短窗口及临时传输 spool。恢复资料必须另行覆盖这些必要配置和密钥的受保护恢复方式；脚本不读取或打包外部秘密。临时传输内容有期限，下载内容丢失时返回过期，不能声称数据库备份恢复了文件字节。

归档包含敏感运维数据，应由部署环境加密存储并限制读取。本轮仅生成临时测试归档，验证后删除；不把归档、真实凭据或生产数据加入仓库/CI artifact。

## 恢复到新空库

先由数据库管理员创建独立空库，配置相应 libpq 环境，确认没有中心连接该目标库。禁止直接覆盖现有库；脚本不创建数据库、不删除现有对象，也不切换服务流量。

```sh
# PGDATABASE 必须已指向这个新的空库。
python3 scripts/database-backup.py restore /private/backups/sideria-20261006 \
  --target sideria_restored
```

目标实际库名必须与 `--target` 一致，且不同于备份源库名。先验证归档大小/校验和和主版本，再使用官方 [pg_restore](https://www.postgresql.org/docs/17/app-pgrestore.html) 输出流，在一个 PostgreSQL 事务内恢复并完成以下隔离：

- 原任务、Step、Execution、Attempt ID 和结果保留；pending/running/uncertain 统一待确认，并追加恢复审计，不自动重放。
- 原审计逐行保留，新恢复审计继续使用恢复后的序列。
- 所有主机旧凭据及注册令牌撤销、身份代次增加；浏览器会话清除。项目、分析和已完成任务保留。
- 非空目标、损坏归档、恢复中途错误或隔离失败使事务回滚。只使用可信来源的归档，不能把任意 SQL 备份当作安全输入。

恢复后先在隔离配置中启动中心并检查任务和审计。设置页保留已撤销主机的稳定 ID 和待核对提示，常规监控列表不把它们当作已接入主机；核对后可在该页显式重新绑定。旧 Agent 无法直接连接；必须核对 Agent journal、目标资源实际状态、备份时间之后发生的操作，再明确授权重新绑定。重新绑定会继续隔离旧代次任务，不是“再执行一次”的按钮。核对完成后，才能由操作员决定是否重新发起业务操作或切换服务。

当前实现是逻辑快照恢复，没有连续 WAL 归档/PITR，也没有自动恢复被丢失的外部副作用事实。RPO 是最近有效备份快照之后的变更；不能用一次小数据恢复耗时承诺生产 RTO。

## 可重复的隔离恢复验证

```sh
docker compose -f compose.test.yaml up -d --wait
export SIDERIA_TEST_DATABASE='postgres://postgres@127.0.0.1:55432/sideria_test?sslmode=disable'
export SIDERIA_TEST_POSTGRES_CONTAINER=sideria-postgres-1
go test -race -v ./internal/server -run TestPostgresBackupRestore -count=1
```

测试使用官方 PostgreSQL 容器内客户端（脚本 `--container` 选项），创建三个随机独立测试库，结束后删除。包含 80 个五种状态任务、144 条原始审计、项目、分析和身份数据；逐条比较任务身份/结果及完整原始审计，检查恢复后的隔离。另验证非空目标、校验和错误、校验和一致但被截断的归档均不留下部分恢复。

2026-10-06 本地样本：约 30,984 bytes 归档，备份约 447 ms、恢复及隔离约 304 ms，总用例约 2 秒。该小样本用于正确性检查，不是容量或服务恢复 SLA。CI 每次在新数据库执行，并上传不含秘密的验证日志。

## 有界负载与真实资源限制

```sh
export SIDERIA_TEST_REDIS='redis://127.0.0.1:56379/0'
SIDERIA_TEST_AGENTS=8 node scripts/fixture.mjs
# 另一个终端：必须使用上面创建的临时 fixture。
node scripts/load.mjs
```

测试上限固定为 8 个本地 Agent 进程，客户端并发 8；每个文件 256 KiB，200 次初始读取、64 个下载、32 个上传，同时继续读取，然后杀死中心并验证 8 个待执行任务同 Attempt 恢复。它们共享一台 Linux 云容器的 CPU/磁盘/网络，不是八台真实服务器。传输内容逐一核对 SHA-256；所有生成资源仅在临时目录和测试数据库中。

| 测量 | 修复前 | 修复后本轮样本 |
| --- | --- | --- |
| 64 个下载 | 35 成功、29 因 429 失败 | 64 成功、0 失败 |
| 下载 P95 完成等待 | 386 ms，但包含快速失败 | 12.39 s，全部完成并核对内容 |
| 32 个上传 | 成功；客户端处理 17 次 429 | 成功；客户端处理 19 次 429 |
| 长传输中的读取 | 共 208 次，74 次未成功返回文件证据 | 216 次成功、0 失败；P95 8.06 ms |
| 中心恢复 | 8 个待执行任务同 Attempt | 全部重连 1.58 s，任务完成 2.13 s |
| 中心/8 Agent RSS 观测峰值 | 约 22.4 / 105.1 MiB | 约 23.6 / 114.0 MiB |

读取修复前的错误用例与首轮 429 基线为不同轮次。原始 JSON：[首轮](evidence/load-before.json)、[背压修复后](evidence/load-backpressure.json)、[共享执行槽混合负载](evidence/load-shared-workers.json)、[最终混合负载](evidence/load-after.json)。指标是本机短时样本，采样 RSS 每 200 ms，可能漏掉瞬间峰值；读取 P95、传输排队时长不能直接推算公网性能。

实测改动：中心 429 在读取请求体/提交内容之前拒绝准入；Agent 在原 Attempt 内最多重试 30 次准入、约 1 秒加抖动间隔，取消/期限生效。网络错误、权限错误和文件副作用不套用该重试。查询最多 8 个并发，与后台任务最多 4 个并发分开，避免长传输占用全部读取额度。

每 Agent 可通过环境独立配置：

| 设置 | 默认与边界 |
| --- | --- |
| `SIDERIA_TRANSFER_BYTES_PER_SECOND` | 1,048,576 B/s；64 KiB/s 至 1 GiB/s；上传与下载共享 token bucket，burst 32 KiB |
| `SIDERIA_TRANSFER_MAX_BYTES` | 32 MiB；可降低至 1 byte，不可超过中心上限 |
| `SIDERIA_TRANSFER_CONCURRENCY` | 2；支持 1–4 |

预算是文件 payload 的速率，不含 HTTP/TLS/重试开销；任务授权仍有五分钟期限，低带宽配置必须相应降低最大文件，不能保证极慢链路传完 32 MiB。不要把用户尚未核实的“2M/30M”标记直接换算为配置。

## 本轮不扩大范围的决定

32 MiB 是已公开的首版容量限制，原文件设计要求大文件/断线边界验证，并未规定无限大小或必须断点续传。本轮保留上限，补充中断、大小、摘要、覆盖和重试边界测试，不引入新的传输服务。

监控 MVP 明确允许有界短窗口不足时显示实际覆盖。默认 5 秒/360 点覆盖约 30 分钟；1 秒采样只能覆盖约 6 分钟。中心重启保留最新快照但不保留内存曲线，当前界面不伪造缺失历史。8 Agent 测量未证明需要独立时序存储；暂不增加 Redis/数据库历史镜像，也不承诺 24 小时原始指标或 30 天聚合已交付。

仍未运行真实双主机、真实模型、真实 TLS 部署、生产数据库/凭据恢复、较长浸泡、公网链路及企业规模压力测试。本轮边界回归不是生产安全审计。
