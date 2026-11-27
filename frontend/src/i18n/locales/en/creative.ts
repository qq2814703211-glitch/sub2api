export default {
  creative: {
    title: 'Creative Plaza',
    subtitle: 'Creative Plaza · Toonflow Web Studio × Quick Image Studio',
    intro:
      'Integrates the real open-source HBAI-Ltd/Toonflow-app Web studio (infinite canvas, 3D TrumanMap pre-visualization, document workspace, AI Assistant with MCP/A2A, and Plugin Market) alongside Quick Image Studio. Projects, 3D WebGL rendering, frame extraction, and assets run strictly inside your browser with zero server compute load; model calls reuse your own gateway API keys.',
    localComputeBadge: '100% Browser Compute · Zero Server Load',
    tabs: {
      toonflow: 'Toonflow Studio',
      imagegen: 'Quick Image Studio',
    },
    quickNav: {
      home: 'Home',
      workspace: 'Infinite Canvas',
      trumanMap: '3D TrumanMap',
      pluginMarket: 'Plugin Market',
      explore: 'Explore',
      fullscreenEnter: 'Fullscreen',
      fullscreenExit: 'Exit fullscreen',
      openNewTab: 'Open in new tab',
    },
    keys: {
      panelTitle: 'Sub2API Gateway Key Sync',
      toggleShow: 'Gateway Keys',
      toggleHide: 'Hide Keys',
      textKey: 'Text / Agent Key',
      imageKey: 'Image Key',
      videoKey: 'Video Key',
      audioKey: 'Audio / TTS Key',
      selectPlaceholder: 'Select an API key',
      fallbackFirst: 'Auto-reuse first active key',
      syncedSummary: '{keys} key(s) synced · {models} model(s) discovered',
      noKeysHint: 'No active API keys found. Create one in API Keys or configure a custom provider inside Toonflow Settings.',
      refreshKeys: 'Refresh keys & models',
    },
    actions: {
      saveAsset: 'Save asset',
      sendToCanvas: 'Send to Toonflow',
      sentToToonflow: 'Imported into Toonflow browser-local Asset Library',
    },
  },
}
