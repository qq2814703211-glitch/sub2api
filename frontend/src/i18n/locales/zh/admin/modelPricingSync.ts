export default {
  modelPricingSync: {
    title: 'models.dev 价格同步',
    description:
      '后台定时拉取 models.dev 官方参考价，写入 pricing.override_file 覆盖层并热生效。只补/改我们价表里还没有手动定价的模型；已手工配置的模型绝不覆盖，分组价卡优先于同步价。',
    empty: '暂无同步记录',
    loadFailed: '加载同步记录失败',
    statusLoadFailed: '加载同步状态失败',
    disabledHint: '未配置 pricing.override_file，同步任务不会启动。',
    columns: {
      appliedAt: '生效时间',
      model: '模型',
      field: '字段',
      change: '从 → 到',
      reason: '原因',
      source: '来源'
    },
    filters: {
      model: '模型',
      modelPlaceholder: '按模型名筛选',
      field: '字段',
      allFields: '全部字段',
      source: '来源',
      allSources: '全部来源'
    },
    fields: {
      input_cost_per_token: '输入价',
      output_cost_per_token: '输出价',
      cache_read_input_token_cost: '缓存读',
      cache_creation_input_token_cost: '缓存写'
    },
    status: {
      title: '同步状态',
      enabled: '已启用',
      disabled: '未启用',
      lastRun: '最近一次执行',
      lastApplied: '本次应用变更',
      interval: '轮询间隔',
      overrideFile: '写入目标',
      never: '尚未执行',
      lastError: '最近错误',
      changes: '{count} 项'
    },
    priceUnitHint: '价格单位为 USD / 1M tokens（覆盖层按 USD / token 存储，此处已换算）。'
  }
}
