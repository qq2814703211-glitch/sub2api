# Grok 订阅同步：合并交接

## 交付位置与范围

- 独立工作树：`/Users/dev/Global-session/xiao-20261102-grok-subscription/final`
- 分支：`fix/grok-subscription-sync`
- 开发基板：`172e847650e266e16844b79ba44cc8ee0b0ace47`
- 最终实现提交：`b53e27fee3b5da1a0505ee40a2ee3fb8c69b04a1`（其后仅文档收尾提交）。
- 后续由管理员与其他 worktree 合并成统一新版本。此任务未推送、未改版本号、未部署、未修改线上账号数据。

## 已确认的生产问题

只读核对的125个Grok OAuth账号中，92个实时返回Free、33个返回Heavy；本地均保留Heavy。Grok-Ultra-13、Grok-Ultra-20的Free同时通过settings、live user subscription和订阅记录交叉核对。账号名本身不决定套餐。

官方PR5571的JWT刷新修复已在生产基板中；它在新token没有tier时保留旧值。官方PR8343为用量窗口调度候选，无法填补实时订阅缺口。本功能沿用现有路径，接入xAI官方客户端同样使用的settings实时显示字段，保存独立订阅观测。

## 行为变化

- 实际刷新周/月用量时，同时查询当前订阅；账单和订阅分别记录成功/失败。
- 手动账号测试查询当前订阅，普通请求通过有界后台任务刷新，不阻塞原响应流。
- 显式Free更新旧Heavy；Ultra和未知新名称保留上游显示名。上游仍报Heavy的账号继续显示Heavy。
- 列表徽章、用量格、既有Free判断和调度精简投影一致；前端避免因观测时间变化反复强制探测。
- 窄字段持久化保留AT/RT及其它配置；迟到观测、重新授权、token正常旋转、账号保存和代理编辑交错有专门保护及回归。
- 使用现有JSON extra存储，无数据库迁移；未新增自动停号或订阅通知渠道。

## 验收证据

- 初始故障行为红测：期望free、实际supergrok_heavy；修后通过。
- 前端父级集成复跑：5文件122项通过；子代理扩展回归11文件154项通过。完整前端build通过（含i18n、vue-tsc、Vite）。
- 首轮后端集成：9907个顶层测试通过、34跳过、1失败；包含子测试19678通过。唯一失败与生产基板相同，为资源敏感的`TestInflightEstimate_AccountMappingNoDBAndBoundedMemory`，单独执行可通过；原始失败保留。
- 修复审查后的父级真实PostgreSQL/race：23个顶层测试、含子测试75项全部通过，零跳过；包含调度缓存往返、scope迁移、managed写入口、代理未提交事务commit/rollback。
- 修复审查后的go vet通过。最后完整后端集成、构建及独立差量复审结果在本交接完成前补入下节。

完整日志、初次审查与修复记录在工作树上级任务目录，分别为`backend-tests.*`、`parent-race.*`、`review-*.md`及`*-fix-progress.md`。业务内设计文档：`docs/grok-subscription-sync.md`。

## 最终验收

最终实现提交`b53e27fe`上的六包全量：9920个顶层通过、34跳过、1失败；含子测试19714通过、34跳过、1失败。唯一失败与基板相同，新失败集为空。原始输出`backend-tests.accepted.log`和机器汇总`backend-tests.accepted.summary.json`保留全部失败与跳过，未将跳过记为通过。

后端构建退出0；实际`-version`回读commit精确为b53e27fee3b5da1a0505ee40a2ee3fb8c69b04a1，版本文件保持0.2.19-GloryCode-0.0.38。该二进制是源码验收产物，未用于生产。

两轴复审`review-final-spec.md`、`review-final-standards.md`逐项确认原4项问题fixed、无open阻断。最后插件快照兼容检查由完整suite发现，行为红绿与单点独立复审`review-plugin-boundary.md`均通过，最终suite中该用例通过。

生产只读收尾回读`production-unchanged-readback.json`确认容器ID、镜像、版本、完整commit均为原身份且healthy；未执行生产写入。合并时可按独立分支整体纳入，VERSION由后续统一发版安排。
