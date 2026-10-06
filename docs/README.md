# Sideria 文档索引

更新日期：2026-10-06

本目录维护巡星的设计文档。当前均处于设计阶段，没有以文档存在代替实现或验证结果。

## 阅读顺序

1. [整体架构](architecture.md)：定位、组件、资源与共用执行路径。
2. [AI 分析与建议](features/ai.md)：MVP 的核心 AI 能力与行为边界。
3. [前端界面](design/ui.md)：单机仪表台、状态光、AI 与终端布局；按关注功能阅读 features，按实现问题阅读 design。
4. [实施与验收](roadmap.md)：MVP 边界、阶段和验收要求。

## 功能

| 文档 | 维护内容 |
| --- | --- |
| [AI](features/ai.md) | 读取工具、证据、解释、建议与后续 Execute |
| [主机 Agent](features/agent.md) | 注册、身份、安装、能力与更新 |
| [监控](features/monitoring.md) | 资源、磁盘、上下行及数据新鲜度 |
| [文件](features/files.md) | 目录、预览、传输与权限 |
| [终端](features/terminal.md) | PTY 会话与 SSH 边界 |
| [Git](features/git.md) | 项目登记、分支、工作区状态与受控写操作 |
| [Docker](features/docker.md) | 容器管理与 Compose |
| [代理](features/proxy.md) | 网络检查与代理作用范围 |
| [安装](features/installation.md) | 系统服务与安装模板 |
| [配置变更](features/configuration.md) | Diff、快照、并发检测与回滚 |
| [Kubernetes](features/kubernetes.md) | 集群连接器、资源与操作边界 |
| [告警](features/alerts.md) | 规则、通知与恢复 |

## 共用设计

| 文档 | 维护内容 |
| --- | --- |
| [前端界面](design/ui.md) | 深夜天文台视觉、四大指标、不对称布局、状态光与响应式 |
| [前端架构](design/frontend.md) | 技术职责、组件、数据流、目录与首个代码切片 |
| [功能页面](design/pages.md) | 八张概念图的信息结构、交互与阶段取舍 |
| [资源模型](design/resources.md) | Host、项目、Runtime、Cluster 与 Workspace |
| [通信与工具](design/communication.md) | 连接、Action、查询、会话、上报和任务请求 |
| [Redis 与可靠性](design/reliability.md) | 幂等、限流、并发租约、背压与故障处理 |
| [任务与审计](design/tasks.md) | 步骤、目标、尝试、恢复和证据 |
| [权限与审批](design/security.md) | 权限、风险、授权与失效 |
| [数据与秘密](design/storage.md) | PostgreSQL、生命周期、Secret Store |
| [部署与容量](design/deployment.md) | 技术选型、发布、资源影响与环境待确认 |

[技术依据](references.md)集中保存原始参考链接，不代表已经实测所有外部能力。

## 维护方式

- 一个功能在对应文档中维护细节，共用机制引用 design，不复制完整规则。
- 全局组件或边界变化时同步 architecture；阶段变化同步 roadmap；新增文档补充索引。
- 功能文档说明范围、阶段、边界、验收与待确认项。概念模型、候选 Action 不冒充正式接口。
- docs 是长期设计文档，随项目版本维护并纳入 Git；实际提交和推送仍需单独授权。
- 本目录不存放凭据、运行日志、截图缓存或一次性脚本；本次不创建 TODO 或改写项目行为规则。
