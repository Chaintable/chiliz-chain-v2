- `[2026-08-19][done] 将 50M gas cap 限定在历史 replay` — state retention 需要新增临时 parent state 上的系统合约调用，但不能改变正常同步的共识执行参数。
  **Decision:** `getTotalSupply` 和 validator 查询继续请求原有的 `MaxUint64/2`；仅由 `callOnState` 在 historical replay 分支通过 `CallDefaults` 和 `GasPool` 限制为 50M。Tokenomics 公式、validator 结果和 state transition 保持不变。
  **Done:** 三个系统合约调用点已恢复原参数；Parlia replay/validator 定向测试及 core historical/normal-path、retention/restart 定向测试通过。lihe-dev 已运行对应代码镜像 `378d04ce@sha256:90f0e46f334a17aaf3bb2eefec17ae80c74bcc4641b4d305fd486f79da52964f`：graceful restart 后从高度 36,785,644 恢复并继续同步，最近 28,800 块的 `trace_debankBlock` 成功且内部 state-root 校验通过，restart=0、OOM=false。

- `[2026-08-19][open] TestGetNewSupplyForBlock 存在随机边界失败` — 测试随机取样可能得到 3.20，但 year 6 的断言下界为 3.21；本次改动未修改公式函数，且该测试不在本次 gas cap 范围内。
