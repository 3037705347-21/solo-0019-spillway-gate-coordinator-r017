# 泄洪闸门操作协调服务

## 项目目标

该服务用于水工控制室协调泄洪闸门的命令申请、安全联锁复核和单次开度调整。系统在同一闸门上只允许一条已通过复核且尚未结束的执行命令，任何冲突都必须被显式拒绝，不能覆盖已经占用的闸门。

## 使用角色

- 运行协调员：登记闸门并提交开度调整命令。
- 安全复核员：提交联锁结论，决定命令是否通过。
- 闸门操作员：凭执行令牌启动命令并登记执行结果。
- 授权运维人员：以 `ops-` 前缀身份提交故障处置依据，解除闸门票锁存。

## 核心实体

- `gate`：闸门及其当前开度、运行状态、最大开度、活动命令、锁存故障票据和最近一次故障清除记录。
- `gate_command`：一次开度调整申请，包含目标开度、申请原因、状态和执行令牌。
- `interlock_decision`：安全复核结论，状态为 clear 或 blocked。
- `fault_clearance`：一次正式故障解除，记录故障命令、处置人、处置依据和解除时间，仅在 faulted -> idle 成功时生成。

## 业务流程

### register-gate

运行协调员通过 `POST /gates` 登记闸门。服务校验标识、名称、最大开度和初始开度，写入空闲状态；重复标识返回稳定冲突错误，不覆盖原记录。

### request-gate-command

运行协调员通过 `POST /commands` 提交开度调整命令。服务读取闸门当前开度和限制，校验目标开度与申请信息，创建待复核命令并冻结起始开度。

### review-safety-interlock

安全复核员通过 `POST /commands/{id}/interlock` 提交复核结论。clear 结论会原子占用闸门并签发一次执行令牌；blocked 结论会把命令置为 rejected。其他命令先占用同一闸门时，当前 clear 结论返回冲突且不改变闸门。

### execute-gate-command

闸门操作员通过 `POST /commands/{id}/execute` 提交执行令牌。服务校验命令、令牌、闸门占用关系和目标开度，在临界区内完成 approved、executing、completed 或 aborted 的转换，并同步更新闸门状态。令牌在命令结束后失效，不能复用。

### clear-gate-fault

授权运维人员（`ops-` 前缀身份）通过 `POST /gates/{id}/clearance` 提交本次故障的处置依据（处置说明与故障命令标识）。服务在同一把仓库锁内校验：闸门必须仍处于 faulted 锁存、清除依据必须引用闸门锁存的那条故障命令、该命令必须为 aborted 且终态原因与闸门票据一致（故障事实明确），并且闸门上不存在任何未结束命令。全部满足时原子恢复为 idle 并写入清除记录，保留最终开度与故障/清除审计票据；依据不足、身份不具备权限、重复清除、并发清除或绕过清除直接下达新命令都返回稳定错误且不改变状态。

## 状态与规则

- `gate` 状态：idle、reserved、moving、faulted。
- `gate_command` 状态：pending、approved、rejected、executing、completed、aborted。
- 只有 pending 命令可以复核。
- 只有 approved 命令可以执行，且执行令牌必须匹配。
- clear 复核与闸门占用必须同时成功；任一步失败时两者都不写入。
- blocked 复核使命令 rejected，不会占用闸门。
- 模拟故障会令命令 aborted、闸门 faulted，并保留最终开度，同时写入显式故障票据（故障命令、终态原因、开度、时间）。
- 已完成或已中止的命令不能再次执行。
- 只有 faulted 闸门可以解除；解除必须由 `ops-` 身份提交充分处置依据，且引用的故障命令与闸门票据一致、命令为 aborted 且终态原因一致。
- 解除时闸门上不能存在任何未结束命令（pending、approved、executing），即使活动命令引用已清空也要扫描拒绝。
- 解除是一次性的守卫转换：重复清除与并发清除中除唯一赢家外都返回冲突，faulted -> idle 与清除记录在同一临界区写入。
- 解除后闸门保留最终开度并恢复调度；新命令以该开度冻结起始开度，闸门读取仍可追溯最近一次清除记录。

## 模块边界

- `cmd/server`：进程入口、监听地址和退出控制。
- `internal/httpapi`：HTTP 路由、JSON 解码、状态码映射。
- `internal/coordination`：工作流编排、命令标识和执行令牌签发。
- `internal/domain`：实体、不变量和状态转换规则。
- `internal/storage`：带互斥保护的内存仓库和原子写入。
- `internal/contracts`：API 请求、响应和稳定错误结构。
- `internal/bootstrap`：运行时依赖装配。

## 接口

- `GET /health`
- `POST /gates`
- `GET /gates/{id}`
- `POST /commands`
- `GET /commands/{id}`
- `POST /commands/{id}/interlock`
- `POST /commands/{id}/execute`
- `POST /gates/{id}/clearance`

错误响应统一为：

```json
{"code":"stable_error_code","message":"可读说明"}
```

## 验证计划

本初始化基线刻意不生成单元测试、夹具或浏览器测试文件，也不声明 `test_command`。后续任务阶段负责补充测试，并在该阶段建立红绿验证。当前每条工作流通过 `cmd/workflowcheck` 启动真实 HTTP 服务并调用公开接口，校验成功状态、持久化结果和关键失败分支。
