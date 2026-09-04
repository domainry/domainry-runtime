# <Capability> 模块

状态：提议 / 开发中 / 已实现  
Owner：  
实现仓库：  
SDK 仓库：

## 职责边界

- 拥有：
- 不拥有：

## 公共合同

- `ApplicationRef`：
- `Factory/Binding`：
- `Descriptor/协议版本`：
- 稳定错误：
- 可选能力接口：

## Module 形态

- Factory：
- ModuleHost：
- 数据表与 migration owner：
- HTTP Adapter：
- worker 生命周期：

## SaaS 形态

- Remote Factory/transport：
- 服务入口：
- 数据与 worker owner：
- 身份认证与 audience：
- timeout/retry/idempotency：

## Runtime 接入点

- `runtimehost.Options`：
- Bootstrap：
- Application/domain adapter：
- Shutdown/readiness：

## 一致性与切换

- Runtime ↔ Module 事务：
- Runtime ↔ SaaS 一致性：
- Module → SaaS：
- SaaS → Module：
- 回滚：

## 验证与代码证据

- SDK contract test：
- Module test：
- SaaS test：
- Runtime composition/external compile test：
- 数据、worker、scope 清单：

## 已知缺口

- 无 / 列明尚未实现项。

