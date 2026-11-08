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
    priceUnitHint: '价格单位为 USD / 1M tokens（覆盖层按 USD / token 存储，此处已换算）。',
    pricingSource: {
      title: '计价口径配置',
      description:
        '同步按三档优先级取价，数字小的档位让位给数字大的档位。手动配置永远最高，同步任务不会覆盖它。',
      priorityLabel: '优先级（低 → 高）',
      highestBadge: '最高',
      tierUsd: 'USD 同步',
      tierUsdNote: 'models.dev 官方参考价，最低优先级',
      tierCny: 'CNY 官方价',
      tierCnyNote: '国模人民币原币牌价，覆盖 USD',
      tierManual: '手动配置',
      tierManualNote: '本卡片配置，永不被同步覆盖',
      loading: '加载口径配置中…',
      loadFailed: '加载口径配置失败',
      endpointMissing: '后端接口 {path} 尚未注册，本卡片暂时只读。',
      endpointMissingHint: '同步任务仍按现有配置运行；接口上线后刷新本页即可编辑。',
      retry: '重试',
      unsaved: '有未保存改动',
      usdTitle: 'USD 同步',
      usdHint: 'models.dev 参考价同步任务的当前状态，只读。',
      usdEnabled: '已启用',
      usdDisabled: '未启用',
      cnyTitle: 'CNY 官方价',
      cnyHint: '国模厂商按官方人民币原币取价，不使用美元换算。',
      sourceUrl: '数据源',
      manualTitle: '手动覆盖',
      manualHint: '按「元 / 百万 tokens」填写。命中的模型同步时直接跳过，不会写入任何价格。',
      manualEmpty: '还没有手动覆盖价。需要锁定某个模型的价格时，点「添加一行」。',
      manualPriorityNote: '此项优先级最高，同步不会覆盖',
      addRow: '添加一行',
      removeRow: '删除此行',
      colModel: '模型键（vendor/model）',
      colModelPlaceholder: 'deepseek/deepseek-flash',
      col: {
        input: '输入价',
        output: '输出价',
        cache_read: '缓存读取价'
      },
      vendorsTitle: '国模厂商清单',
      vendorsHint: '（{count} 个，走 CNY 官方价）',
      save: '保存',
      saving: '保存中…',
      saved: '计价口径已保存',
      saveFailed: '保存计价口径失败',
      errFixFirst: '请先修正标红的行',
      errModelRequired: '模型键不能为空',
      errModelFormat: '模型键需为 vendor/model 格式（含斜杠）',
      errModelDuplicate: '模型键重复',
      errPriceRequired: '必填',
      errPriceInvalid: '需为非负数'
    }
  }
}
