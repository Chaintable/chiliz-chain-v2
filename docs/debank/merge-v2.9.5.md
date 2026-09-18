# Chiliz v2.9.5 upstream merge 验证报告

日期：2026-09-10。同步、状态、COR-193历史重放及优雅重启验证完成；上游 invocation hook 带来的 trace/event ID 和 validation hash 变化已逐项核对，供 PR review。

## 1. 主要更新与升级理由

建议升级。官方将 v2.9.5 定义为 **Mandatory fleet upgrade**：全部 validator 使用修复版本前，治理不能再次执行 DeployerProxy runtime upgrade。我方 archive writer 也受此次修复影响，旧版本不能正确重放特定历史升级块。[官方发布说明](https://github.com/chiliz-chain/v2/releases/tag/v2.9.5)

COR-193 修复 `EVM.Call` 调用 runtime-upgrade hook 时遗漏的 DeployerProxy warm access。旧代码对后续访问多收 2,500 gas，主网 31,384,697、Spicy 32,196,267 的重放与 canonical receipt 不一致。新工具 `replaycheck` 从 RPC 读取历史输入，以本地 binary 重放并检查交易 gas、Parlia fee pool 和系统支付。

本次实际源码基线是已合入 fork 的 **v2.8.1 → v2.9.5**，共 713 个上游变更文件，包含 BSC v1.6.3 → v1.7.3 的 Parlia、EVM、tracing、存储与依赖更新。v2.9.5 与 v2.9.5-rc1 指向同一 commit，但先前的版本台账不代表 fork 已合入这些代码。主网 embedded chain config 未变化，没有新增主网激活时间。

## 2. 合并冲突与保留的 fork 行为

基于 fork `cf0e8fd2e`，合入 upstream `746c4e76a`。先通过 tree 不变的 merge `71c66e256` 接回之前 squash 丢失的 v2.8.1 ancestry，再形成源码 merge `71d4094a0`。以实际 v2.8.1 基线合并有 13 个文件冲突，其中 3 个涉及业务代码，另有 CI 和依赖文件。

保留的业务偏离项：

- `trace_debankBlock`：向 ETL 返回 block file、header、完整 state diff 和 validation hash。它使用 canonical `StateProcessor.Process` 执行交易与系统调用；返回前检查重放状态根等于区块头，失败时不交付产物。
- `StateDB.StateDiff`：保留账户、storage、code、销毁记录。`IntermediateRoot` 先完成块内最终状态；storage 的 `pendingStorage`/`originStorage` 在 Commit 前仍可读取，能保留 slot 删除及最终值，而不是只提供 state root。
- 历史 Parlia 查询：`HistoricalStateReplay` 路径在执行前保存 parent state，每次系统合约查询使用独立副本，避免读取当前链头或污染下一次查询。正常导块路径不启用这一包装。只读系统查询保留 50M gas 上限。
- 可配置 trie retention：保留 `TriesInMemory`、历史状态重建及退出时的持久化边界，供需要较长历史窗口的 fork 使用；本轮 archive 部署沿用生产参数。
- 保留 fork 的双架构 build/release 工作流与 `github.com/Chaintable/pipeline v0.0.66` 依赖，为镜像发布和 ETL 产物提供支持。

上述保留项已适配新的 StateProcessor、chainContext、ToMessage 和 trie 接口。upstream 对 DeployerProxy 的 OnEnter/OnExit 与 hook depth 有变化，运行时对账必须检查其具体影响。

## 3. 测试部署

从新鲜的生产 seed 快照恢复到独立测试卷，使用与生产相同的 `full/archive/hash` 模式及数据目录结构。仅运行 node；生产 ETL 通过 `trace_debankBlock` 拉取产物，测试直接调用该 API 验证完整产物，没有配置生产投递服务或凭证。

测试镜像固定为 `public.ecr.aws/b2h7a5c4/chaintable/chiliz-writer:b0209e8e` 的 amd64 digest。`b0209e8e` 是 PR 虚拟 merge commit，已确认其 tree 与本地测试过的源码 merge `71d4094a0` 完全相同。容器限 8 GiB、4 CPU，日志轮转 100m × 5；首启前清除恢复卷中的生产 nodekey。

2026-09-10 08:56:20 UTC 启动。恢复卷 800 GiB，文件系统已用 675 GiB、可用 112 GiB；快照后台初始化速率 300 MiB/s。完整 compose 见本节附录。

```yaml
name: chiliz-upstream-v295
services:
  node:
    image: public.ecr.aws/b2h7a5c4/chaintable/chiliz-writer:b0209e8e@sha256:0cb6b92b1ca3fad45be13c035132f1db0d39091b229f154846f79df778632654
    platform: linux/amd64
    container_name: chiliz-upstream-v295-node
    user: "0:0"
    restart: unless-stopped
    stop_grace_period: 2m
    cpus: 4
    mem_limit: 8g
    memswap_limit: 8g
    entrypoint:
      - /usr/local/bin/geth
      - --chiliz
      - --datadir=/var/data
      - --ipcdisable
      - --history.transactions=0
      - --http
      - --http.addr=0.0.0.0
      - --http.port=8545
      - --http.api=eth,web3,personal,debug,net,debank,trace
      - --http.corsdomain=*
      - --http.vhosts=*
      - --syncmode=full
      - --gcmode=archive
      - --state.scheme=hash
      - --metrics
      - --pprof
      - --pprof.addr=0.0.0.0
      - --pprof.port=6060
      - --rpc.gascap=250000000
      - --maxpeers=200
      - --verbosity=3
      - --port=30312
    volumes:
      - type: bind
        source: ./data
        target: /var/data
        bind:
          create_host_path: false
    ports:
      - "127.0.0.1:28855:8545"
      - "127.0.0.1:28856:6060"
      - "30312:30312/tcp"
      - "30312:30312/udp"
    logging:
      driver: json-file
      options:
        max-size: 100m
        max-file: "5"
    networks:
      - chiliz-test
# The node exposes the full production trace API. No ETL service or publisher
# credentials are supplied; validation reads trace_debankBlock directly.
networks:
  chiliz-test:
    name: chiliz-upstream-v295-net
    ipam:
      config:
        - subnet: 10.99.72.0/24
```

## 4. 验证结果

已完成：

- Go 1.25.5 `go build ./...` 通过；CI 使用 Dockerfile 中的 Go 1.26.4，amd64、arm64 与 manifest job 全部成功。[CI 34455979501](https://github.com/Chaintable/chiliz-chain-v2/actions/runs/34455979501)
- `core/state`、`core/vm` 两包完整测试通过，共 131 个顶层测试。上游原有生成预期数据用例 `TestWriteExpectedValues` 保持原有 skip，没有新增跳过。
- core/Parlia/replay 定向回归的 9 个顶层测试通过，包含 retention=128/300、zero-default、restart、parent-state 隔离、正常导块查询路径、系统查询 gas 上限，以及主网/Spicy 的 6 份重放 fixture。
- StateDiff 专项检查通过：非零 slot 更新、slot 删除、事务内 revert、后续交易恢复原值、账户销毁、当块创建再销毁、code 更新和 root 检查。所有账户/storage/code 数据都参与断言。
- 读取主网 31,384,697 的真实历史输入，本地合并 binary 重放三笔交易和系统支付全部匹配。首笔 canonical/本地 gas 均为 **1,450,973**，现网 v2.8.1 的远端重放为 **1,453,473**；系统支付为 **2,901,946,000,000,000**。该用例直接验证 COR-193 的 2,500 gas 修复。

追块段 37,385,907..37,385,926 的 20 块 hash/root 全部一致；最初 5 个系统交易块完整 trace 一致。补充用户交易样本后，37,385,914 / 37,385,924 / 37,385,925 分别多出 2 / 4 / 2 条 invocation hook trace。

新增调用全部符合 upstream `applyChilizInvocationEvmHook`：miner 调用 `0x…7005.checkContractActive(parent.to)`，gas 上限 1M、零 value、无状态写入。该 hook 的 gas 不从用户调用 gas 中扣除；用户交易 receipt gas 与状态 diff 不变。新增调用占据调用树位置，导致后续 trace/event 的 ID、父关联、位置、子调用数以及 validation hash 变化。

已按 pipeline v0.0.66 的 ID、位置和 validation hash 算法逐项核对；在独立归因检查中，仅投影掉上述严格识别的 hook 并重建派生字段后，其余全部输出一致，包含完整 RLP 六字段。**原始 trace 与旧生产不相等**，这不是普通排序差异；保留上游新增产物，需在 PR review 中接受该变化。历史块或新旧版本混跑的 validation hash 比较会出现差异。

恢复节点也已成功调用主网 31,384,697 的 `trace_debankBlock`，三笔交易 gas 与 canonical receipt 一致，状态根检查通过；旧生产完整 API 报系统支付不匹配。首次请求超时，重试成功，原始记录保留。

最终运行时结果：

| 检查 | 结果 |
|---|---|
| 追块段 37,385,907..37,385,926 | 20 块 hash、parentHash、stateRoot、transactionsRoot、receiptsRoot 全部匹配 |
| 追块段完整产物 | 8 个不同区块：5 个系统交易块完整输出一致；3 个用户交易块的 invocation hook 差异全部解释，状态 diff 一致 |
| 近 head 37,421,360..37,421,379 | 20 块五项 hash/root 全部匹配 |
| 近 head 完整产物 | 37,421,360 / 361 / 362 / 364 / 368 分别新增 3 / 9 / 1 / 10 / 1 条 invocation hook；派生字段变化全部解释，无剩余差异；完整状态 diff 一致 |
| 首次追平 | 09:32:59 UTC，首启后约 36 分 39 秒；09:33:22..09:34:10 连续 5 次与生产及官方高度完全一致、syncing=false |
| 优雅重启 | 09:34:44 收到退出信号、09:34:46 Blockchain stopped；恢复高度 37,421,369 与退出前一致，固定块 37,421,329 的五项 hash/root 未变 |
| 重启后采样 | 09:36:20..09:37:07 连续 5 次 syncing=false；生产 lag=0、官方 lag=0..1 |
| 最终观察 | 高度 37,421,416，相对快照新增 35,510 块；0 次自动重启、0 OOM；内存约 2.06 GiB（观察到的重启前占用约 3.8 GiB），数据盘剩余 112 GiB |

共检查 40 个不同区块的 hash/root、13 个不同区块的常规完整 trace/state_diff，另检查 COR-193 历史块。8 个用户交易块有明确的上游产物变化，原始 trace 不记为逐字节一致。比较保留全部业务字段，仅忽略 `block_file.block.process_start_timestamp`，对无序 collection 排序后比较全部六个 RLP state-diff 字段。

## 5. 后续事项

- 新旧版本对包含 invocation hook 的同块输出会生成不同 trace/event ID 和 validation hash。发布时应将这项变化纳入 writer/seed 与下游一致性校验的切换安排；不能将此类差异直接解释为共识分叉。
- 原生产参数包含未注册的 `personal`/`debank` HTTP 模块，并让独立 metrics 与 pprof 争用 6060；新旧版本的实际 RPC 模块都是 `debug/eth/net/rpc/trace/web3`，且 pprof 的 `/debug/metrics` 实测可读。它们是原配置的冗余项，本次没有减少 RPC 或 metrics 能力。
- PR 由用户 review/merge；使用 merge commit 保留上游历史。之后构建正式 release 镜像并交付切换 runbook，生产 rollout 由 SRE 执行。
