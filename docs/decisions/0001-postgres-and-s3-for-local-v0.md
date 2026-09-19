# 0001：个人 V0 使用 PostgreSQL 与 S3 兼容对象存储

已采纳。虽然 SQLite + 本地文件能让个人项目少运行两个服务，V0 仍选择 PostgreSQL 保存 Project、Conversation、Run、持久事件和已提交 Artifact metadata，使用 S3 兼容对象存储保存输入及制品，本地由 MinIO 提供。真实事务/锁承担 Run 调度、取消与 Session 提升的一致性，对象存储和独立制品网关分开处理不可变文件与浏览器访问；这也避免未来远程 Sandbox 把宿主机文件路径变成长期业务接口。

代价是本地 Compose、迁移、bucket 初始化、备份和跨数据库/对象存储的清理更复杂，上传对象与产品事务也不能作为一个原子事务提交。因此保留先上传、后提交 metadata、再对账清理的顺序，而不是把上传成功等同产品成功。若项目长期仅离线单机使用且运维成本明显超过收益，可重新评估 SQLite/本地文件；若开始多人部署或接入远程 Sandbox，再按实际需求评估托管 PostgreSQL/S3，不先引入更多基础设施。

设计依据：[中文](../superpowers/specs/2026-07-19-harness-forge-design.zh-CN.md) / [English](../superpowers/specs/2026-07-19-harness-forge-design.md)。
