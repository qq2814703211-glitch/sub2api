export default {
  "title": "异步视频结算",
  "worker": "启用后台结算",
  "all": "全部状态",
  "refresh": "刷新",
  "help": "任务沿用创建时价格。恢复结算仅查询已有任务，不会重新生成视频；没有上游任务编号的记录需人工核对。",
  "loading": "正在加载任务…",
  "empty": "暂无视频任务",
  "task": "任务 / 模型",
  "owner": "用户 / Key / 账号",
  "state": "状态 / 上游 / 尝试次数",
  "amount": "结算金额",
  "updated": "更新时间",
  "action": "操作",
  "retry": "恢复结算",
  "more": "加载更多",
  "loadError": "视频任务加载失败",
  "saveError": "任务或后台设置更新失败",
  "states": {
    "creating": "创建中",
    "submitted": "等待完成",
    "ready": "待扣费",
    "billed": "已扣费，待写用量",
    "done": "已结算",
    "failed": "失败",
    "expired": "已过期",
    "reconciliation": "待核对"
  }
}
