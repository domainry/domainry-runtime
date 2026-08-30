# Data Exchange 模块

状态：Module 与 SaaS 已接入  
Owner：大文件 import/export job、chunk、artifact、queue scope 与处理生命周期  
实现/SDK：`domainry-data-exchange` / `domainry-data-exchange-sdk`

## 边界与模式

Runtime 通过 `DataExchangeFactory` 选择进程内 large-file engine 或 Remote Binding。ModuleHost 向能力暴露登记后的 import/export provider、数据库、migration 与 workspace context；能力不允许直接访问 Runtime 任意 repository。SaaS Remote transport 通过 SDK `saashost.Transport` 完成 submit、query、cancel、download。

## 代码接入

- 组合：`pkg/runtimehost/options.go`、`pkg/runtimehost/external_module_test.go`
- Host：`runtime/bootstrap/runtime/data_exchange_module_host.go`
- SDK：`domainry-data-exchange-sdk/sdk.go`、`modulehost`、`saashost`
- Module/Remote：`domainry-data-exchange/module`、`domainry-data-exchange/remote`
- File engine：`domainry-data-exchange/fileengine`

## 变更约束

- provider key 是 Host 授权边界；未知 provider 必须拒绝，不能取得通用 Runtime service container。
- artifact ownership、下载授权、TTL、取消、重试和断点恢复必须在两种模式保持合同等价。
- SaaS 模式需显式设计对象存储交接、上传/下载凭证时效及大 payload 不进入消息/日志的规则。

