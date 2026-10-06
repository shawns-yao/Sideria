# Kubernetes 集群管理

更新日期：2026-10-06  
状态：设计阶段，尚未实现和验证

- 使用独立集群身份访问 Kubernetes API。
- 获取节点、工作负载、事件和服务等资源状态。
- 在相应权限开放后，支持部署、扩缩容、日志和容器终端。
- 使用受限 ServiceAccount 和 RBAC，不默认授予 `cluster-admin`。
- 使用 list/watch 获取资源变化，处理重连和重新同步，避免高频全量轮询。

集群不必绑定某台主机 Agent；集群操作由具有独立身份和权限的连接器承担。连接器可以与主机 Agent 部署在同一机器上，但凭据和操作作用域仍分离。

## Docker 与 Kubernetes 混合管理

| 操作 | 选择目标 | 执行入口 |
| --- | --- | --- |
| 主机文件、磁盘、终端、系统服务 | 一台服务器 | 主机 Agent |
| Docker/Compose 部署 | 一台具备对应能力的服务器 | 主机 Agent |
| Kubernetes 部署 | 集群与命名空间 | Kubernetes API |
| Kubernetes 日志与容器终端 | 集群、命名空间、Pod、容器 | Kubernetes API |
| Kubernetes 节点限制 | 节点标签与调度约束 | Kubernetes 调度器 |

服务器保存能力集合和集群关联，不使用单个互斥字段将主机固定为 Docker 或 Kubernetes 类型。服务器与 Node 的关联保存集群 ID、Node UID 和明确绑定依据，不仅凭 IP 猜测。

应用部署到 Kubernetes 时通常选择集群和命名空间，由调度器安排节点。特定架构、磁盘或地区要求使用标签和亲和性表达。节点容量判断还要考虑资源请求、可分配资源和现有负载，不能只看瞬时 CPU。

第一阶段接入已有集群，提供节点、工作负载、事件和状态查询。自动建集群、加入或移除节点、控制平面升级属于后续独立能力，需要单独评估网络、存储和业务影响。

## 实施与验收

本功能不属于首版 MVP。此处“第一阶段接入”指 Kubernetes 功能自身的首阶段，先接入已有集群只读状态，后续再开放写操作。

校验集群身份、命名空间与 RBAC，覆盖连接器断线、权限不足、Node 与 Host 绑定和 list/watch 重新同步。宿主机 Agent 在线不代表集群 API 健康。

AI 的集群建议随对应读取工具开放；工具尚不可用时不得虚构 Pod、事件或资源容量。

## 关联文档

- [整体架构](../architecture.md)
- [统一工具与通信](../design/communication.md)
- [权限与审批](../design/security.md)
- [资源模型](../design/resources.md)
