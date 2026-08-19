- `[2026-08-19][done] 将 50M gas cap 限定在历史 replay` — state retention 需要新增临时 parent state 上的系统合约调用，但不能改变正常同步的共识执行参数。
  **Decision:** `getTotalSupply` 和 validator 查询继续请求原有的 `MaxUint64/2`。正常路径仍经 `ethAPI.Call` 使用节点配置的 `RPCGasCap`（lihe-dev 未覆盖参数，实际 50M；生产 writer 参数为 250M）；仅 historical replay 的直接 EVM 路径由 `callOnState` 通过 `CallDefaults` 和 `GasPool` 固定限制为 50M，避免执行量无上限，也不引入依赖墙钟的 timeout。两者差异只在调用执行上限，不修改 Tokenomics 公式、validator 解码结果或 state transition。
  **Done:** 三个系统合约调用点已恢复原参数；Parlia replay/validator 定向测试及 core historical/normal-path、retention/restart 定向测试通过。主网实测 `getMiningValidators` 约 1,050,350 gas、`getTotalSupply` 约 23,972 gas，均远低于 50M。lihe-dev 已运行对应代码镜像 `378d04ce@sha256:90f0e46f334a17aaf3bb2eefec17ae80c74bcc4641b4d305fd486f79da52964f`：graceful restart 后从高度 36,785,644 恢复并继续同步，最近 28,800 块的 `trace_debankBlock` 成功且内部 state-root 校验通过，restart=0、OOM=false。

- `[2026-08-19][open] TestGetNewSupplyForBlock 存在随机边界失败` — 测试随机取样可能得到 3.20，但 year 6 的断言下界为 3.21；本次改动未修改公式函数，且该测试不在本次 gas cap 范围内。

- `[2026-08-19][done] 实测本次 graceful restart 后最差 historical replay` — 重启前 head 为 36,785,644；对重启前保留范围 36,755,645–36,785,642 的 state 可用性逐块扫描，并执行本次重启最大重放距离对应的 `trace_debankBlock(36,785,643)`。
  **Decision:** 本次重启的实际最差距离是 5,361 块，不是理论上限 30,000 块；36,755,645–36,780,281 的 state 均可直接打开，之后 5,361 个 parent state 不可直接打开，最近可用落盘 state 为 36,780,281。它来自正常运行期间的 trie 落盘，而不是 shutdown 显式新增的 `{HEAD, HEAD-1, HEAD-(N-1)}`；不同重启时点的实际最大重放距离仍会随最近可用落盘 state 变化。
  **Done:** state regeneration 内部耗时 39.054s，完整 RPC 耗时 43.942s，返回 112,902 bytes、`validation_hash=318982`，每个 replay block 的 state-root 校验均通过。观测容器内存由 4.923GiB 升至最高 7.093GiB（+2.170GiB），CPU 最高 160.58%，容器 `restart=0`、`OOM=false`，无 state-root mismatch、out-of-gas、panic、Fatal 或 ERROR 日志；测试后节点继续同步。
