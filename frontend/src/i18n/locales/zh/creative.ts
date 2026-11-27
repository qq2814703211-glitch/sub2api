export default {
  creative: {
    title: '创意广场',
    subtitle: '创意广场 · Toonflow 网页端 × 快捷生图',
    intro:
      '直接集成开源 HBAI-Ltd/Toonflow-app Web 端完整创作台（无限画布、3D 白模预演 TrumanMap、文档工作区、AI 助手 MCP/A2A、插件市场）与原有快捷生图能力。所有工作区工程、3D 渲染、抽帧与本地素材均在你的浏览器本机运算与存储，不占用服务器性能；模型调用复用你的站内 API 密钥。',
    localComputeBadge: '纯浏览器本机运算 · 零服务器算力',
    tabs: {
      toonflow: 'Toonflow 创作台',
      imagegen: '快捷生图',
    },
    quickNav: {
      home: '首页',
      workspace: '无限画布',
      trumanMap: '3D 白模预演',
      pluginMarket: '插件市场',
      explore: '探索模板',
      fullscreenEnter: '全屏创作',
      fullscreenExit: '退出全屏',
      openNewTab: '独立窗口打开',
    },
    keys: {
      panelTitle: '站内网关密钥同步',
      toggleShow: '站内密钥配置',
      toggleHide: '收起密钥配置',
      textKey: '文本 / AI 助手密钥',
      imageKey: '图像生成密钥',
      videoKey: '视频生成密钥',
      audioKey: '音频 / TTS 密钥',
      selectPlaceholder: '选择站内 API 密钥',
      fallbackFirst: '默认自动复用首个密钥',
      syncedSummary: '已同步 {keys} 个站内密钥 · 发现 {models} 个模型',
      noKeysHint: '暂无可用站内密钥，请先前往「API 密钥」页面创建，或在 Toonflow 设置中配置自定义供应商。',
      refreshKeys: '刷新密钥与模型',
    },
    actions: {
      saveAsset: '存素材库',
      sendToCanvas: '发到 Toonflow',
      sentToToonflow: '已导入到 Toonflow 浏览器本机素材库（快捷生图导入）',
    },
  },
}
