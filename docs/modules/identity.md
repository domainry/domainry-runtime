# Identity 模块

状态：Module 与 SaaS 已接入  
Owner：账号、认证、用户、角色、权限、session 与应用授权目录  
实现/SDK：`domainry-identity` / `domainry-identity-sdk`

## 边界与模式

Runtime 通过 `runtimehost.Options.IdentityFactory` 注入 Factory，并在 `pkg/runtimehost/identity_integration.go` 打开 Binding。Module 必须实现 SDK HTTP Adapter provider，向 Runtime 暴露 browser authentication 与 management adapter；SaaS Binding 必须不返回进程内 Adapter。两种模式都通过 `Descriptor` 校验 protocol、policy bundle、catalog、issuer/audience 和 mode。

Identity 状态不属于 Runtime。Module 可以使用 Runtime 提供的项目数据库 handle，并保留 Identity source-owned 表命名空间；所有迁移必须通过宿主 migration registrar 提交，复用宿主唯一 `_schema_migrations`，不得创建 Identity 私有迁移账本。SaaS 状态由远端 Identity 服务持有。组织机构、人员主职和汇报关系均由 Identity 自己维护；Runtime 只提供业务 Profile 等项目数据事实，不接管 Identity 授权模型。

## 代码接入

- 组合：`pkg/runtimehost/options.go`、`pkg/runtimehost/external_module_test.go`
- 生命周期与 Adapter：`pkg/runtimehost/identity_integration.go`
- Runtime 投影：`pkg/runtimehost/identity_integration.go`
- SDK 合同：`domainry-identity-sdk/sdk.go`、`authorization/principal`
- Module：`domainry-identity/module`
- SaaS Remote：`domainry-identity-sdk/remote`

## 变更约束

- 新路由必须进入 SDK HTTP Adapter 合同并声明 exposure；不得从 Runtime router 直接 import Identity handler。
- Module 的 DDL/DML 必须使用 `domainry-orm`；只有 ORM 无等价能力时才允许局部 raw SQL，并附方言测试与理由。
- 新授权事实先归属 Identity 或 Runtime business profile owner，再通过窄 resolver 投影，不能复制两份真相。
- Module/SaaS 切换必须校验 issuer、audience、角色目录 publication 和 session/token 行为。
- 外部编译验收必须证明 SaaS 项目不依赖 `domainry-identity/module`。

## 已知差异

进程内 HTTP Adapter 是 Identity Module 的明确差异，不是所有能力的通用要求。SaaS 浏览器流量由远程 Identity endpoint 承担。
