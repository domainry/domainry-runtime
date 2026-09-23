# Metadata 模块

状态：独立源码 Module 已接入；尚未形成 Runtime 可选 SaaS 拓扑  
Owner：定义版本、workspace preference、workspace rule set 与 Metadata 自有持久化  
实现/SDK：`domainry-metadata` / `domainry-metadata-sdk`

## 当前边界

Runtime bootstrap 固定构造 `domainry-metadata/module.Factory`。Module 借用宿主数据库、SQL 方言和 migration registrar；所有 source-owned migration 写入宿主唯一 `_schema_migrations`。Runtime 负责读取项目 Manifest、组合跨 owner 校验并将已授权定义投影给 Metadata，不得重新建立第二套 Metadata 可写真相。

`/metadata/*` 只由 Metadata Module HTTP Adapter 发布 definition、localization 与 dictionary 读取。Runtime 自身的应用结构校验、migration plan、record count 与诊断统一位于 `/application-schema/*`，`source_owner=appschema`；Provision manifest 生命周期位于 `/provision/manifests/*`。Capability 汇集由 Plane 的 source registry 负责，Runtime 不再发布 `/capabilities`、`/metadata/capabilities` 或 `/metadata/execution-capabilities`。

## 代码接入

- Bootstrap：`runtime/bootstrap/runtime/startup.go`
- Host：`runtime/bootstrap/runtime/metadata_module_host.go`
- Runtime projection：`runtime/bootstrap/runtime/metadata_restoration.go`
- Module/SDK：`domainry-metadata/module`、`domainry-metadata-sdk`

## 已知缺口

- `runtimehost.Options` 尚无 Metadata Factory，当前不能宣称支持 SaaS 切换。
- 多实例 snapshot revision/invalidation 必须通过共享数据库集成测试后才可发布。
- Runtime 的 `_project_model_state` 是已安装 model.json 的单行当前状态，不是 Metadata Module 的第二份可写真相；定义内容仍以 Metadata Module 的 projection 为准。
