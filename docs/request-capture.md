# 定向短时请求采集

默认关闭；管理入口为“请求采集”（`/admin/request-captures`）。使用现有管理员角色，不引入 ID1 特权。受限管理密钥不获得新权限，正文预览、正文导出始终要求真人管理员会话最近完成 TOTP step-up；不受全局可选 step-up 开关影响，相关读取进入现有审计日志。

## 实例与配置

- 为每个可同时运行的实例设置唯一且重启稳定的 `SUB2API_CAPTURE_INSTANCE`（例如槽位名）。没有显式槽位时存储不可启用；不要给蓝绿实例复用同一目录。
- 目录为 `${DATA_DIR:-./data}/request-captures/<slot>/`，目录0700、文件0600。`.instance` 保存稳定UUID，`.owner.lock` 阻止另一进程同时打开该槽位。
- 负载均衡必须提供固定实例的管理入口；UI发现UUID后为操作附带 `X-Capture-Instance`，到错实例返回409，不自动换实例重试。此标头不会替你配置负载均衡。
- 配置保存在 `settings` 的 `request_capture_config:<slot>`，默认 enabled=false、quota_mib=1024、retention_days=7。保存即时作用于本实例，无全局轮询器；各槽位独立设置。停用立刻拒绝新会话并停止任务，收尾后worker没有定时器、存储查询或业务工作。再次启用只允许创建新任务，不复活旧任务。

## 数据与生命周期

- 用户／分组／账号目标，时长1–1440分钟。分组按鉴权后的入口分组匹配；实际路由分组另记。账号目标在真实出站选择后匹配，其他账号只记尝试摘要，不写其正文。未匹配任务不落正文。
- 记录四段应用层报文：client_request、upstream_request、upstream_response、client_response；包含账号、请求关联ID、HTTP尝试及WS握手/逐轮次。不是网卡抓包或传输字节重放。HTTP压缩内容在现有解压流程后采集，原有PrereadBody零拷贝快路径保持。
- 定向任务仅发布已收尾、确认失败的记录，包括曾有失败尝试但最终恢复成功的请求；正常和未确认结果删除。**处理中可能先写临时正文，成功不保留不等于从未写盘。**
- Header采用白名单；JSON/SSE结构化认证字段、URL凭据/签名脱敏，媒体默认保存摘要，明确勾选才保留媒体正文。自由文本、工具输出、提示词不能保证完整脱敏，按敏感数据管理，不复制进报告或普通日志。
- 有界队列、64MiB采集缓冲、最多256会话／16任务、每条128MiB、同时两个导出；配额/磁盘/索引错误停止或跳过采集，不阻塞或重放业务请求。界面显示partial、skipped和存储错误。
- 手动停止／到期／关闭开关会收尾；整任务导出须停止且无在途引用，单条已完成失败记录可独立导出。流式tar.gz导出需要HTTPS下支持文件系统保存的浏览器，不在浏览器内存聚合全量归档。
- 重启保留实例身份，运行中任务标记interrupted，不自动恢复；成功、待完成、孤儿正文在恢复时清理，已确认失败可回读。保留期从任务结束起算；启动、启用期间每分钟清理过期任务，停用期间不做后台清理，读取/导出仍拒绝超过保留期的正文。停用期间若需立即释放空间，使用任务删除或在停机后按卸载流程删除目录。

## 转发包装顺序

注册顺序：外层Recovery → 现有poison.Middleware → 鉴权 → RequestCapture → 模型/正文改写 → 业务。账号选定后poison.WrapWriter包住采集writer，实际输出是业务 → poison检查/改写 → capture记录已交付字节 → 原始writer。采集中间件收尾先调用幂等poison.Finalize，再关闭流并Finish，保证保留尾部和拦截结果完整。

HTTP：原有取消作用域 → 捕获出站请求 → servertiming.Do → 原有poison原始/解码观察 → 解压 → 捕获可读上游响应 → 原有cancelOnCloseBody。无重放，不替换poison证据。

WS：入口ReadOpenAIWSClientMessage读取原始帧，池lease/直连FrameConn记录实际出站帧与回包；下游在写成功后记录恢复模型/工具名后的帧。独立下游生命周期context只附加同一采集会话值，不替换取消来源。

与connection-diagnostics合并时建议其无正文writer位于capture外侧；两者收尾均必须在poison.Finalize之后。该lane不依赖或修改pkg/requesttiming、pkg/requestoutcome，transport元数据辅助类型位于internal/requestcapture/transportdiag。

## 卸载清单

1. 在每个槽位关闭采集、停止任务；等待active_requests归零，按需导出后删除任务。
2. 停止服务，移除本功能的middleware/HTTP/WS接线、管理API/侧栏/路由、service初始化与cleanup；删除internal/requestcapture和独立UI/API/locale文件。保留poison原模块。
3. 删除各槽位request-captures目录（包括.instance、owner lock）；不删除其他DATA_DIR内容。
4. 经备份确认后删除request_capture_records、request_capture_tasks及settings中的request_capture_config:<slot>。**已应用迁移910文件不可改写或移除**，未来卸载使用新的加法迁移，保留checksum历史。

来源归属见internal/requestcapture/NOTICE.md。无远端推送、无生产部署。
