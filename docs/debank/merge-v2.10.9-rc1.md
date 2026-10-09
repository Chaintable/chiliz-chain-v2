# Chiliz v2.10.9-rc1 upstream merge 验证报告

日期：2026-10-09。合并编译测试、测试节点同步段 hash 抽样、与生产 writer 的 `trace_debankBlock` 对比和优雅重启均通过。

## 1. 主要更新与升级理由

主网不是强制升级。官方说明写明这是 **Spicy testnet** 的强制升级（DeployerProxySunset、Snake8Fix 两个分叉在 Spicy 于 2026-10-08 激活），并写明 "Mainnet schedules neither fork. On mainnet, 2.10.9 is a client update with no activation."。主网 embedded chain config 和 `genesis` submodule 指针都没有变化。[官方发布说明](https://github.com/chiliz-chain/v2/releases/tag/v2.10.9-rc1)

本次合并 v2.9.5 → v2.10.9-rc1（上游 #135，245 个文件）。对主网有效的内容：

- BSC v1.7.4 → v1.7.8：Chapel/BSC 的 Pasteur 分叉只写在 BSC 配置里；其余为 RPC 限制、filters/downloader/discover 修复、freezer fd 泄漏等 bugfix，以及删除 BAL、multidb、overflow pool、fake-beacon。
- COR-184：Dragon8 supply 的 Tokenomics 读取改为按 parent hash 定位，且只在 Dragon8（非 Dragon8Fix）分支调用。原实现按块号解析，节点 canonical 索引有缺口时会把合法区块判为无效。
- COR-174（Snake8 snapshot LRU 原地修改）、COR-213（Snake8 VFQ 结构化解析）、COR-250（opcode optimizer 数据竞争）。

该 tag 是 RC（GitHub 未标 prerelease）。

## 2. 合并冲突与保留的 fork 行为

基于 fork main `7355fba20`（v2.9.5-ct.1），合入 upstream `8ed372eda`，merge commit `55ee4f040`。merge-base 是 v2.9.5。冲突 3 个业务文件、6 个上游 workflow。

保留的 fork 行为（与 v2.9.5 相同，未改动语义）：

- `trace_debankBlock`：经 canonical `StateProcessor.Process` 重放，返回 block file、header、完整 state diff 和 validation hash；返回前校验重放 state root 等于区块头。
- `StateDB.StateDiff`：账户、storage、code、销毁记录。上游删除了相邻的 BEP-592 access list 函数，`StateDiff` 原样保留。
- 历史重放时 Parlia 系统合约查询读取执行前保存的 parent state（`HistoricalStateReplay`），只读查询 50M gas 上限；正常导块不走这条路径。`getLastSupplyFromTokenomics` 的冲突在这里：保留 replay 分支，普通 RPC 分支采用上游的 parent hash 定位，调用位置随上游移入 Dragon8 分支。
- 可配置 trie retention、fork 的 build/release workflow、`github.com/Chaintable/pipeline v0.0.66` 依赖。

测试适配：上游在 `parlia.New()` 中新增断言，chain config 同时设置 Plato 和 Feynman 时校验 BSC 专有的 validator-set 方法选择器（COR-39）。fork 的 3 个 replay 测试原先用 `params.ParliaTestChainConfig`（两者都设置）构造 engine，改为去掉 FeynmanTime 的副本；Chiliz 不排 Feynman，主网配置不触发该断言。上游同类 BidBlock 测试是 `t.Skip`。

无需人工裁量的决策点。

## 3. 测试部署

生产节点已在 production 集群，参数为 `--gcmode=full --state.scheme=hash --triesInMemory=30000 --history.blocks=600000`，内存上限 16Gi。测试节点参数照抄生产 writer（只加 `--port`），从生产 seed 2026-10-07 的快照恢复独立测试卷，首启前删除恢复卷中的 nodekey。只运行 node：生产投递在独立 ETL 容器里经 `trace_debankBlock` 拉取，测试直接调用该 API，没有部署 ETL，也没有生产投递凭证。

测试镜像 `chiliz-writer:27f7734a`（amd64 digest 固定）。`27f7734a` 是 PR 的 GitHub 测试合并提交，tree 与 merge commit `55ee4f040` 完全相同。[CI 37872845725](https://github.com/Chaintable/chiliz-chain-v2/actions/runs/37872845725) 的 amd64、arm64、manifest 三个 job 均成功。容器 16 GiB / 4 CPU，日志轮转 100m × 5。2026-10-09 02:13 UTC 启动，快照高度 38,192,117（约落后 1 天 22 小时）。

```yaml
name: chiliz-upstream-v2109rc1
services:
  node:
    image: public.ecr.aws/b2h7a5c4/chaintable/chiliz-writer:27f7734a@sha256:2fcf989cf46f9b43763b32d27df4a46dbe0a48c5365210c4df8f22d5dab2b883
    platform: linux/amd64
    container_name: chiliz-upstream-v2109rc1-node
    user: "0:0"
    restart: on-failure:5
    stop_grace_period: 2m
    cpus: 4
    mem_limit: 16g
    memswap_limit: 16g
    # Production node arguments (production/blockchain-chiliz nodex-node-8828b9fd, 2026-10-09)
    # plus --port for the test host.
    entrypoint:
      - /usr/local/bin/geth
      - --config=/etc/config/http-timeouts.toml
      - --chiliz
      - --datadir=/var/data
      - --ipcdisable
      - --history.transactions=30000
      - --history.blocks=600000
      - --http
      - --http.addr=0.0.0.0
      - --http.port=8545
      - --http.api=eth,web3,personal,debug,net,debank,trace
      - --http.corsdomain=*
      - --http.vhosts=*
      - --syncmode=full
      - --gcmode=full
      - --state.scheme=hash
      - --triesInMemory=30000
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
      - type: bind
        source: ./http-timeouts.toml
        target: /etc/config/http-timeouts.toml
        read_only: true
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
# Node only: production delivery runs in a separate ETL container, which is not
# deployed here. Validation reads trace_debankBlock directly.
networks:
  chiliz-test:
    name: chiliz-upstream-v2109rc1-net
    ipam:
      config:
        - subnet: 10.99.73.0/24
```

## 4. 验证结果

- 构建与测试：本地 `go build ./...` 通过，`go mod tidy -diff` 无差异；`consensus/parlia`、`core`、`core/state`（含 snapshot）、`core/vm/...`、`eth`、`params`、`config`、`internal/replay` 测试全部通过，包含 fork 的 replay/retention 回归测试和上游 COR-184 测试 `TestGetLastSupplyFromTokenomicsPinsParentByHash`。
- 追平按用户决定不作为本次门槛（正确性由下列同步段抽样确认）。02:13 UTC 启动后约 1 小时导入 3.4 万块，卷初始化完成后约 700 块/分钟。

| 检查 | 结果 |
|---|---|
| 区块 hash（与官方 RPC） | 追块段起点 38,192,118–137、中段 38,206,215–234、重启后 38,226,000–019，共 60 块全部一致 |
| `trace_debankBlock`（与生产 writer 同块） | 11 块全部一致：5 个合约调用块（58–80 条 trace、11–17 条 event）、5 个多交易块（6–12 笔）、1 个仅系统交易块，范围 38,220,335–38,224,743 |
| `trace_debankBlock` 自检 | 追块段 7 块成功返回并通过节点内 state root 校验，含 epoch 块 38,217,600（其 stateRoot 与官方一致，覆盖 epoch 块 validator 校验读 parent state 的路径） |
| 优雅重启 | `docker compose stop` 20.3 s 退出（exit 0），按预期写入 HEAD、HEAD-1、HEAD-29999 状态；重启后从 38,225,981 继续，固定块 38,225,856 的 hash/stateRoot 不变，继续导块，0 次自动重启、0 OOM |
| 资源 | 追块期间内存约 9.9 GiB / 16 GiB；数据盘剩余 148 GiB（82% 已用） |

trace 对比口径：只忽略节点本地处理时间 `process_start_timestamp`；`storage_contracts` 按集合比较；`state_diff` 解码为 RLP 六字段（Hash、ParentHash、NewAccounts、DeletedAccounts、StorageDiff、NewCodes）后按集合比较（Go map 遍历顺序不确定，两版本都如此）；其余字段（含 validation_hash）逐字节一致。生产 writer 只在内存中保留最近 30000 块状态，trace 对比样本只取该窗口内的块，避免让生产节点重建历史状态。

启动日志中的 `Unavailable modules in HTTP API list [personal debank]` 和 metrics 与 pprof 争用 6060 端口来自生产启动参数，v2.9.5 已记录，与本次合并无关。

## 5. 后续事项

- 生产数据盘（2026-10-09 只读 df）：writer 62.4G 已用 73%、剩 16.1G；seed 786G 已用 81%、剩 147G。增长速度未测，需要关注。
- 生产 terminationGracePeriod 为 30 s，测试节点优雅退出用时 20.3 s（含 30000 层内存状态中的 3 份落盘），余量不大。
- 目标是 RC tag：合并后是否以该 RC 发 `-ct.N` release，还是等上游 stable v2.10.9，在 release 时确认。
- PR 由用户 review/merge，请使用 merge commit 保留上游历史。生产切换由 SRE 执行，只需替换镜像 tag，启动参数不变。
