# Party 模块

状态：Module 与 SaaS 已接入  
Owner：Party、Person、组织关系以及 team/store/territory/warehouse scope 事实  
实现/SDK：`domainry-party` / `domainry-party-sdk`

## 边界与模式

Runtime 通过 `PartyFactory` 打开 Binding。Module 由 `partySDKModuleHost` 提供数据库与 Identity directory 等窄 Host 能力；SaaS 通过 SDK Remote Binding 获取同一业务能力。Party 自有基础表即使借用 Runtime 数据库仍归 Party；SaaS 模式由远端服务持有。

Runtime 将 Party 的 organization scope facts 投影给 Identity 授权解析，但 Identity 不拥有 Party 组织事实，Runtime 也不得把这些事实复制成第二套可写模型。

## 代码接入

- 组合：`pkg/runtimehost/options.go`、`pkg/runtimehost/external_module_test.go`
- Bootstrap：`runtime/bootstrap/runtime/party_sdk_module_host.go`、`runtime/bootstrap/runtime/startup.go`
- Identity scope bridge：`pkg/runtimehost/identity_organization_scopes.go`
- SDK：`domainry-party-sdk/sdk.go`、`modulehost`、`remote`
- Module/Remote：`domainry-party/module`、`domainry-party-sdk/remote`

## 变更约束

- 组织结构写模型只存在于 Party owner。
- Runtime/Identity 只消费 versioned scope projection；缓存必须有失效/版本策略。
- Module/SaaS 切换需验证 profile binding、组织层级、workspace 隔离与授权结果等价。

