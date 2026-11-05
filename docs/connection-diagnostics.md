# 无正文连接诊断

管理入口：侧栏“连接诊断” `/admin/connections`。默认关闭，仅当前实例热开关；重启回到关闭，不隐式启用正文采集。常规管理员沿用现有角色；受限管理密钥必须显式获授 `usage.connections.read`（查询/状态）或 `usage.connections.write`（启停），原有 usage/ops 权限不会自动扩大。

## 覆盖与边界

- POST `responses`、`responses/compact`、`messages`、`chat/completions`：请求体字节数/耗时，实际下游交付字节数、写错/取消/协议终态，HTTP200但SSE失败与零用量请求独立落库。
- Responses GET WebSocket upgrade：入口连接记录与每轮独立记录。`ProxyResponsesWebSocketFromClient` 在首轮显式开始，并包装已有 BeforeRequest/AfterTurn；每轮终态后即可查询，不等待整条连接关闭。覆盖 ctx_pool、passthrough、HTTP bridge；bridge 如实标注 HTTP 上游。
- HTTP 出站：已有 Do/DoWithTLS caller → egress attempt → 实际 Client.Do transport attempt。httptrace 在标准 transport 可观测处记录 DNS/TCP/TLS/首字节、连接复用。部分自定义 dialer/插件不暴露细分阶段，缺失字段不推导为零耗时。
- WS：实际池建连/直连拨号记录 handshake；池 lease/透传 frame wrapper 记录请求和响应字节计数、首字节/语义事件、逻辑轮次终态；复用连接不会伪造一次新握手。下游模型/工具名恢复后才记成功写入的字节。
- 不宣称覆盖 Gemini 原生、媒体任务、所有长连接或插件内部网络。代理 ID 当前无法从 HTTPUpstream 的 URL 参数可靠映射，保持未知0，不解析/保存代理凭据。
- terminal_flushed 只证明本地写入/flush成功，不能证明远端收到或解析；metadata 仍包含用户/账号/组/IP等身份信息，按管理数据控制访问。

## 存储、开关和生命周期

独立 `request_connection_traces`，迁移909；不依赖 usage_logs，不创建账单、不重试上游。detail 最多128KiB；collector最多32次尝试/路由、256个span。容量256的非阻塞队列；队列满或锁争用记 dropped，写失败和保留清理失败单独计数，在管理状态和页面可见。计数为进程生命周期累计，不是账本或成功率分母。

关闭先停止新入口collector，再取消诊断worker的SQL、等待退出并丢弃旧队列；再次开启创建新worker，不复活旧队列。未启用/关闭后没有诊断业务worker或清理定时器。启用后首次记录按需启动worker，启动与每小时清理最多10000条过期记录；固定保留30天，查询也强制30天读边界。关闭期间可保留物理过期数据但读不到；重新启用后清理。进程清理在关闭数据库前调用 StopConnectionDiagnostics。

只持久化固定状态码、分类错误、分类路由/端点与计数/时间；不保存headers、请求/响应正文、工具参数、raw error、URL query或proxy URL。OutcomeObserver虽读取流中的协议字段，但有界且不保留正文。

## 与 request-capture 合并

本lane无 `internal/requestcapture` 引用，正文采集关闭/未合入也独立编译运行。capture tree保持只读。

HTTP注册：Recovery → poison.Middleware → RequestTiming → 既有鉴权 → RequestCapture → 业务；账号选定后业务写入 → poison检查/改写 → capture writer → timing writer → 原始writer。两个观察器收尾均须先幂等调用poison.Finalize，最后才快照/关闭stream；保留 FlushError/Unwrap 与原取消作用域。

出站：原取消作用域 → capture request → timing StartTransport/servertiming.Do → timing response observer → poison原始/解码证据 → 解压 → capture readable response → cancelOnCloseBody。各wrapper只观察一次，不重放、不改变usage。

WS：本lane的 beginWSConnectionDiagnostics 包装原生命周期hooks；正文lane的原始帧/最终写出帧wrapper继续保留。两者附加context值时都必须保留原取消来源，特别是 relay 使用的独立下游write context：先附加相同诊断/采集会话值，再写出。pool/handshake wrapper各自记录一次，父级按职责组合，不能用capture启停作为timing门。

## 验证与卸载

真实行为测试位于 middleware/connection_diagnostics_test.go、repository/usage_connections_{http,lifecycle,postgres}_test.go、service/openai_ws_connection_diagnostics_test.go、handler/admin/usage_connections_test.go、adminaccess/policy_test.go，以及 ConnectionDiagnosticsView.spec.ts。独占临时DB runner为 scripts/check_connection_diagnostics_db.py；日志与receipt在lane artifacts，报告区分pass/fail/skip。生产供应商请求与最终集成树全量验收留父级执行。

卸载：关闭本实例诊断 → 移除 RequestTiming、HTTP/WS hook、cleanup hook、管理API/权限、路由/侧栏/UI/locales → 移除本lane独立pkg及service/repository文件 → 备份后以新加法迁移删除 request_connection_traces。已应用迁移909必须保留原filename/checksum。保留poison及原账单逻辑；禁止删除其他DATA_DIR数据。

来源：只读fork cdf13f1991c369dbc5a4585ad197c103f0772451；pkg/requesttiming、pkg/requestoutcome、util/transportdiag保留NOTICE。无推送、无部署。
