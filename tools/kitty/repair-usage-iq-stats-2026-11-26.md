# Kitty 用量、智商测试与账号统计本地修复

- 日期：2026-11-26（Asia/Shanghai）；来源：codex。
- 基线：`6f87111`（`0.2.24.3-kitty.6`）。工作分支：`fix/kitty-usage-iq-stats`。
- 状态：本地补丁，未提交、未推送、未发布、未部署、未切流；没有修改生产配置或历史账单。
- 保留原 Kitty 皮肤及二开；没有更换上游基线，也没有改 OpenAI 的 VM2 托管入口。

## 1. 自动智商测试报错，手动测试正常

只读核对到：自动测试使用 `gpt-6.1-sol + minimal`，上游明确 HTTP 400 拒绝 `reasoning.effort=minimal`；手动成功记录用的是 `gpt-6-astra + 默认强度`，返回 21。两次配置不同，不是已经证实的答案提取故障。

- 手动、定时、计划保存统一兼容规则：仅映射后的 `gpt-6.1-sol` 请求 `minimal` 时改为支持的 `low`，不更换模型，不覆盖其他有效强度。
- 新测试同时记录原请求和实际强度；历史组件显示兼容调整。
- 真正的请求失败仍为 error，不评分，不自动重试，不把历史红条洗成成功。
- 定时执行真实错误传递给计划 last_error；前端区分答案匹配率与请求完成/异常数量。

## 2. Gemini 工具调用、用量与缓存

近期零用量样本与客户端正常 tool_use 对应，但缺少这些请求的原始上游 SSE 帧，因此不能把历史每条零值确定归因于同一种问题。

- 按字段合并同一响应内的累计 usageMetadata：省略的 prompt、candidate、thought、cache 不覆盖之前已观察的计数。
- 后续完整全零占位帧不擦除先前非零用量；部分字段明确零仍可更新。非零尾包明确 cache=0 时不擅自取历史最大值。
- 自建 API-key 池与 OAuth 路径都处理 response 包装；messages/chat 及原生 Gemini 的流式、流转非流路径，将已接受的计数对齐客户端与账单。原生路径保留错误、finishReason、未知扩展、工具参数和大整数，不补不存在的 0，不重复正文。
- 对计数及 candidate+thought 合计做持久化范围检查，异常巨大数值不溢出为负数、不截断为伪造费用，保留先前有效证据并记录告警。
- 仅采集有界计数证据，区分未上报、部分上报、明确零及计数回退。不持久化提示词、回答正文、工具参数/ID或凭据。
- 接通已存在 usage_audit 列的单条/批量写入、查询、管理员 DTO 和权限裁剪；普通用户响应不增加此字段。
- 后台不再把未知用量直接解释为免费或确定未命中。正常工具调用不因 usage=0 而被自动重放。
- 保留原价格与倍率规则，包括 hydrogen 缓存读 $0.07/1M。新增碳/氢模型合并用量 → 1x 标准价 → 0.5x 实扣的回归，账号倍率不会再次乘到用户扣费上。

## 3. 整个统计右侧全为 —

根因是批量 handler 原来只返回今日字段，前端读取的 lifetime 字段没有接通。

- 接通永久账号费用台账 kitty_account_lifetime_costs，累计账号费用和用户扣费分别读取 account_cost/user_cost，不再乘当前倍率。
- 今日账号费用沿用既有历史口径：COALESCE(account_stats_cost, total_cost) × 请求时 account_rate_multiplier，倍率只乘一次。NULL account_stats_cost 在原迁移中明确表示使用默认公式，不能误判为费用丢失。用户扣费仍取保存的 actual_cost。
- 数据源/查询失败为未知，只有成功查询确认当日无记录时才返回零。两种来源独立失败，按账号 ID 对应，128 个一批，无逐账号回退扫描。
- 缓存键包含本地日期；前端丢弃过期异步响应，避免分页/刷新错挂金额。
- 进一步确认永久采购收据保留了逐条计量，因此新增 955_kitty_account_lifetime_usage.sql，按 (api_key_id,request_key) 保存独立计数快照并增量汇总。统计宇宙为 receipt + 未被 receipt 替代的 legacy；legacy 用同一复合键关联 permanent_usage，不重复累加 TTL 子项。请求数按身份去重，Token 有任何缺失/异常证据时保持未知。receipt 替代 legacy、后续纠正和换账号均按差额调整，既有费用台账和金额不改。列表只读小汇总表，不扫描所有历史。

## 历史与上线边界

- 不伪造缓存命中，不承诺每轮高命中率。
- 已缺失的上游用量无法从零值精确恢复，不估算回填或补扣旧账单。
- 永久费用按既有台账原样读取，不重新给历史请求定价。
- 本地测试不能代替上线后真实请求验收；本轮没有发送生产付费推理请求。
- usage_audit 使用现有迁移 926 的列；新增 955 仅用于永久请求/Token 计数，尚未在生产执行。
- 955 需要一次性读取永久证据并持有源表写锁。上线前必须备份、在生产规模副本预估回填耗时和额外磁盘、安排停止写入/维护窗口；不能直接在线热跑。锁获取超时 5 秒时事务回滚，不可把锁超时当作回填完成。
- 已在隔离 PGlite（PostgreSQL WASM）执行真实 949 + 955 验证 SQL 语义；这不替代真实多连接 PostgreSQL 的并发、锁等待与生产规模性能验收。

## 验证

- 后端较广定向回归：Gemini / Antigravity / RecordUsage / 智商测试 / 账号统计 / 账号费用定价，2096 个测试事件（含父子用例）通过。排除了下述已在纯 kitty.6 复现的 3 个旧价格测试；这不是全套后端全绿。
- 随后新增合并用量碳/氢模型 0.5 倍计费测试，再跑全部新 Gemini 证据用例通过。
- 最终 repository / DTO / admin 的 usage、audit、request type、summary、永久计数、日期缓存回归共 166 个测试事件通过；另 DTO 全包 47 项通过，迁移结构/安全测试包通过。单条、批量、nil、坏 JSON 以及普通用户不可见审计的权限边界已覆盖。
- 最终前端相关 9 文件、80 项测试通过（含中英文文案编译/键完整性）；vue-tsc -b、17 个变更 TypeScript/Vue 文件 ESLint 通过。
- Vite 正式构建通过，输出到 backend/internal/web/dist；仅有既有大 chunk 和 Browserslist 数据陈旧提示，没有安装/升级依赖。
- 最终后端 embed 构建及 --version 通过，包含原生用量与 955 迁移；本地标记 0.2.24.3-kitty.6-local-fix，输出在 TEMP 独立目录，未覆盖或启动现有服务。SHA256：629396116852669086A822F944823ECC27F292AE2354897D86988365CDD275B2。
- git diff --check 通过；新独立 Go 文件 gofmt 已检查。未为刷格式而改动原账号 handler 的既有排版。

### 已复现的原始基线失败（不是本轮新增）

通过 git archive HEAD backend 在独立临时目录验证，基线为 6f87111。价格与测试断言没有为了刷绿被改动：

1. Gemini 3.8 Flash / Gemini 4 Argon 的 3 个旧测试仍预期上游旧价格；Kitty 从既有 kitty.5 起已有明确自定价格覆盖。Kitty 专用 20 模型 1x/0.5x 用例通过。
2. 3 个管理员旧测试仍期望拒绝 Gemini / Antigravity，但原 Kitty.6 早已支持这两个平台。
3. Repository 的 API-key 分组 fixture 缺表、simple-mode mock 缺既有查询；3 个 PgDumper 用例在当前 Windows PATH 缺少 sh 时失败；HTTP refused trace 受 Windows connectex 错误类型差异影响。

这些问题需要独立维护，不代表本轮功能完全未经验证，也不能宣称全套测试已经通过。此次没有为了消除旧失败而回退 Gemini 支持、既定价格或弱化断言。

## 永久计数 SQL 可复现验证

工具：tools/kitty/verify_account_lifetime_usage.mjs。测试数据库只在本机 WASM 内存中，绝不连接服务器；依赖只安装在 TEMP，不修改项目 package/lock。

```powershell
$runtime = Join-Path $env:TEMP 'kitty-stats-pglite-20261126'
npm.cmd install --prefix $runtime --save-exact --ignore-scripts --no-audit --no-fund @electric-sql/pglite@0.6.0
$env:KITTY_PGLITE_ROOT = $runtime
node.exe tools/kitty/verify_account_lifetime_usage.mjs
```

已验证：真实 949 费用迁移作为前置；955 初始回填不改费用、Token 不完整为未知、legacy→receipt 不重复计数、纠正幂等、换账号、永久计量延后补齐、100.0 整数数字、非法类型/缺省/负数/小数/invalid_metrics、receipt 优先、无账号归属的 permanent-only 不分配、后续新增、重复迁移数值幂等。此外执行了实际 repository SELECT、超过 int64 的总量投影为未知、删除 usage_logs 后计数/费用保留的验证。单连接测试不证明多事务并发安全。

## 上线前必做预检（本轮未执行）

1. 备份数据库与现有镜像/配置，准备应用回滚；不要删除或回滚已有财务台账。
2. 在生产规模副本执行 955，估算快照表与索引占用、WAL、回填耗时。核对 receipt/legacy 身份的财务行存在；行锁仅对既有财务行有效，不会自动补账。
3. 真实 PostgreSQL 多连接下验证正常 usage 写入、legacy 纠正、receipt promotion 与跨账号更新的锁序/并发；PGlite 仅验证单连接SQL语义。
4. 为 955 安排暂停计费写入/维护窗口，避免自动启动应用时直接在流量下执行。955 需要完整事务，失败应回滚而非跳过。
5. 核对计数快照请求数、unknown_requests、金额台账与样本；Token 不完整保持未知。捕获前已不存在的历史不能凭空恢复。
6. 上线后分别验收自动检测参数、工具调用用量证据、各协议客户端与账单一致、原价格卡/0.5倍率实际扣费，以及账号今日/永久四项统计。

代码仅留本地 fix/kitty-usage-iq-stats，尚需明确发布授权、按现有Kitty流程晋升/发布。没有创建提交、合并、tag或GitHub Release。

### 前端全量测试的额外披露

最终完整 Vitest：664 个文件，647 文件通过、17 文件失败；5607 项通过、72 项失败、9 项跳过。不能宣称全套全绿。

用 git archive HEAD frontend 在独立临时目录复核，完整归档运行复现了其中 71 项失败；剩余的管理员权限 i18n 测试读取 backend 权限目录，在补充归档 HEAD 对应目录后也以同一断言失败。按文件和 fullName 比较，72 项失败均可在未带入本轮补丁的 HEAD 中复现。归档因原始忽略文件未纳入等原因另有 6 个组件加载错误，因此只把这次比较用于具体失败的来源判断，不冒充整个基线测试环境完全等价。

这些失败包括旧平台清单、Codex 配置默认、权限文案目录、侧栏/代理及账号全选等既有断言差异；本轮没有为了刷绿修改它们或回退既定 Kitty 功能。

测试日志：TEMP/kitty-frontend-full-final-20261126.json；基线：TEMP/kitty6-frontend-baseline-20261126-a8ff95cf。
