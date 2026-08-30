# Metadata 模块

状态：独立源码 Module 已接入；尚未形成 Runtime 可选 SaaS 拓扑  
Owner：定义版本、workspace preference、workspace rule set 与 Metadata 自有持久化  
实现/SDK：`domainry-metadata` / `domainry-metadata-sdk`

## 当前边界

Runtime bootstrap 固定构造 `domainry-metadata/module.Factory`。Module 借用宿主数据库、SQL 方言和 migration registrar；所有 source-owned migration 写入宿主唯一 `_schema_migrations`。Runtime 负责读取项目 Manifest、组合跨 owner 校验并将已授权定义投影给 Metadata，不得重新建立第二套 Metadata 可写真相。

## 代码接入

- Bootstrap：`runtime/bootstrap/runtime/startup.go`
- Host：`runtime/bootstrap/runtime/metadata_module_host.go`
- Runtime projection：`runtime/bootstrap/runtime/metadata_restoration.go`
- Module/SDK：`domainry-metadata/module`、`domainry-metadata-sdk`

## 已知缺口

- `runtimehost.Options` 尚无 Metadata Factory，当前不能宣称支持 SaaS 切换。
- 多实例 snapshot revision/invalidation 必须通过共享数据库集成测试后才可发布。
- Runtime 旧 Metadata schema/store 清退后必须同步更新 state、workspace scope、retirement 与 idempotency inventory。
