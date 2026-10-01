# 泄洪闸门操作协调服务

这是一个无外部依赖的 Go 后端，用本地内存仓库协调闸门登记、开度调整命令、安全联锁复核和执行结果。服务强调单闸门活动命令互斥、原子状态转换、执行令牌生命周期和稳定错误传播。

## 运行

```sh
go build ./...
go run ./cmd/server --addr 127.0.0.1:18119
```

健康入口为 `GET http://127.0.0.1:18119/health`。

## API

```text
POST /gates
GET  /gates/{id}
POST /commands
GET  /commands/{id}
POST /commands/{id}/interlock
POST /commands/{id}/execute
POST /gates/{id}/clearance
```

登记闸门示例：

```json
{
  "id": "gate-east",
  "name": "东侧泄洪闸",
  "max_aperture": 4.5,
  "initial_aperture": 0.5
}
```

提交命令示例：

```json
{
  "gate_id": "gate-east",
  "target_aperture": 2.25,
  "reason": "上游水位调整",
  "requested_by": "dispatcher-lin"
}
```

联锁复核示例：

```json
{
  "verdict": "clear",
  "verifier": "safety-wu",
  "note": "上下游条件满足"
}
```

执行示例：

```json
{
  "operator": "operator-chen",
  "token": "复核响应中的 execution_token",
  "simulate_fault": false
}
```

执行故障会把闸门锁存为 `faulted`。只有授权运维人员（`ops-` 前缀身份）提交处置依据、引用锁存的故障命令，且闸门没有未结束命令时，才能解除锁存：

```
POST /gates/gate-east/clearance
```

```json
{
  "command_id": "故障执行对应的已中止命令 id",
  "handler": "ops-qin",
  "evidence": "现场人工盘车复核，液压与机械限位恢复正常，已重新挂接安全销",
  "note": "处置完成申请恢复"
}
```

解除成功返回恢复为 `idle` 的闸门（保留最终开度，附带 `fault` 与 `last_clearance` 审计票据）、清除记录和对应的 aborted 命令；依据不足返回 400，非授权身份返回 403，重复/并发清除、引用错误故障或仍有未结束命令返回 409，且均不改变状态。

## 目录

- `cmd/server`：服务进程。
- `cmd/workflowcheck`：五条工作流的真实 HTTP 检查入口。
- `internal/bootstrap`：依赖装配。
- `internal/contracts`：公开数据契约。
- `internal/coordination`：业务工作流。
- `internal/domain`：实体、不变量和状态机。
- `internal/httpapi`：路由、解码和错误映射。
- `internal/storage`：互斥保护的内存仓库。

## 工作流检查

```sh
go run ./cmd/workflowcheck --workflow register-gate
go run ./cmd/workflowcheck --workflow request-gate-command
go run ./cmd/workflowcheck --workflow review-safety-interlock
go run ./cmd/workflowcheck --workflow execute-gate-command
go run ./cmd/workflowcheck --workflow clear-gate-fault
```

这些检查会创建临时 HTTP 服务并调用真实路由。它们不调用外部网络，也不依赖数据库。

## 测试状态

测试有意延后。当前交付仅包含初始化基线和生产级 smoke 检查；后续任务阶段会添加自动化测试与红绿验证，不应把当前没有测试文件视为项目未完成。
