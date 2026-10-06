# 技术依据

更新日期：2026-10-06

- [Vue 3](https://vuejs.org/guide/introduction.html)：前端组件与 TypeScript 使用入口。
- [Vue 项目创建](https://vuejs.org/guide/quick-start.html)：Vue 单文件组件与 Vite 构建；具体 Node 要求按锁定工具版本确认。
- [Responses API](https://developers.openai.com/api/reference/responses/overview)、[函数工具调用](https://developers.openai.com/api/docs/guides/function-calling)与[流式事件](https://developers.openai.com/api/reference/resources/responses/streaming-events)：中心模型接入与兼容性验收依据。
- [Go embed](https://pkg.go.dev/embed)：嵌入前端静态产物。
- [Go SSH](https://pkg.go.dev/golang.org/x/crypto/ssh)：可选 SSH 安装与故障排查。
- [gopsutil](https://github.com/shirou/gopsutil)：基础主机指标采集。
- [xterm.js](https://xtermjs.org/)：浏览器终端。
- [PostgreSQL 文档](https://www.postgresql.org/docs/current/)：中心结构化数据存储。
- [PostgreSQL 备份与恢复](https://www.postgresql.org/docs/current/backup.html)：数据库备份与恢复方案。
- [Docker Engine API](https://docs.docker.com/reference/api/engine/)：容器管理接口。
- [Docker daemon 代理](https://docs.docker.com/engine/daemon/proxy/)与[构建、容器代理](https://docs.docker.com/engine/cli/proxy/)：不同作用范围。
- [Kubernetes 容器运行时](https://kubernetes.io/docs/setup/production-environment/container-runtimes/)：运行时与 Docker 的边界。
- [Kubernetes 节点选择](https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/)与[资源管理](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/)：调度与容量依据。
- [Kubernetes RBAC](https://kubernetes.io/docs/concepts/security/rbac-good-practices/)：集群访问权限。
- [K3s 要求](https://docs.k3s.io/installation/requirements)与[内嵌 etcd 高可用](https://docs.k3s.io/datastore/ha-embedded)：候选实验集群方案的部署边界。

上述资料用于设计参考，不代表本项目已经实现或通过相应能力验收。实施时按选定依赖版本复核接口和支持范围。

返回：[整体架构](architecture.md) · [文档索引](README.md)
