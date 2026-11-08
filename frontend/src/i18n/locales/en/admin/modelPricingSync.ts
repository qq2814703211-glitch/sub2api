export default {
  modelPricingSync: {
    title: 'models.dev Price Sync',
    description:
      'A background job pulls the official reference prices from models.dev, writes them into the pricing.override_file overlay layer, and reloads it live. Only models without a manual price in our table are filled or updated; models with manual pricing are never overwritten, and group price cards always take precedence over synced prices.',
    empty: 'No sync records yet',
    loadFailed: 'Failed to load sync records',
    statusLoadFailed: 'Failed to load sync status',
    disabledHint: 'pricing.override_file is not configured, so the sync job will not start.',
    columns: {
      appliedAt: 'Applied At',
      model: 'Model',
      field: 'Field',
      change: 'From → To',
      reason: 'Reason',
      source: 'Source'
    },
    filters: {
      model: 'Model',
      modelPlaceholder: 'Filter by model name',
      field: 'Field',
      allFields: 'All fields',
      source: 'Source',
      allSources: 'All sources'
    },
    fields: {
      input_cost_per_token: 'Input',
      output_cost_per_token: 'Output',
      cache_read_input_token_cost: 'Cache Read',
      cache_creation_input_token_cost: 'Cache Write'
    },
    status: {
      title: 'Sync Status',
      enabled: 'Enabled',
      disabled: 'Disabled',
      lastRun: 'Last Run',
      lastApplied: 'Changes Applied',
      interval: 'Interval',
      overrideFile: 'Write Target',
      never: 'Never run',
      lastError: 'Last Error',
      changes: '{count} field(s)'
    },
    priceUnitHint: 'Prices are shown in USD / 1M tokens (the overlay stores USD / token; converted here).',
    pricingSource: {
      title: 'Pricing Source Priority',
      description:
        'Sync resolves prices through three tiers. A lower tier yields to a higher one. Manual entries always win and are never overwritten by sync.',
      priorityLabel: 'Priority (low → high)',
      highestBadge: 'Highest',
      tierUsd: 'USD sync',
      tierUsdNote: 'models.dev reference prices, lowest priority',
      tierCny: 'CNY official',
      tierCnyNote: 'Native CNY list prices for Chinese vendors, overrides USD',
      tierManual: 'Manual',
      tierManualNote: 'Configured on this card, never overwritten by sync',
      loading: 'Loading pricing source config…',
      loadFailed: 'Failed to load pricing source config',
      endpointMissing: 'The backend endpoint {path} is not registered yet, so this card is read-only for now.',
      endpointMissingHint: 'Sync keeps running with the current config; reload this page once the endpoint ships.',
      retry: 'Retry',
      unsaved: 'Unsaved changes',
      usdTitle: 'USD sync',
      usdHint: 'Current state of the models.dev reference price sync job. Read-only.',
      usdEnabled: 'Enabled',
      usdDisabled: 'Disabled',
      cnyTitle: 'CNY official',
      cnyHint: 'Chinese vendors are priced in their native CNY list prices, never converted from USD.',
      sourceUrl: 'Source',
      manualTitle: 'Manual overrides',
      manualHint: 'Prices are CNY per 1M tokens. A model listed here is skipped by sync entirely — no price is written.',
      manualEmpty: 'No manual overrides yet. Add a row to pin a model price.',
      manualPriorityNote: 'Highest priority, sync never overwrites this',
      addRow: 'Add row',
      removeRow: 'Remove row',
      colModel: 'Model key (vendor/model)',
      colModelPlaceholder: 'deepseek/deepseek-flash',
      col: {
        input: 'Input',
        output: 'Output',
        cache_read: 'Cache read'
      },
      vendorsTitle: 'Chinese vendor list',
      vendorsHint: '({count} vendors priced from CNY official lists)',
      save: 'Save',
      saving: 'Saving…',
      saved: 'Pricing source saved',
      saveFailed: 'Failed to save pricing source',
      errFixFirst: 'Fix the highlighted rows first',
      errModelRequired: 'Model key is required',
      errModelFormat: 'Use the vendor/model format (must contain a slash)',
      errModelDuplicate: 'Duplicate model key',
      errPriceRequired: 'Required',
      errPriceInvalid: 'Must be a non-negative number'
    }
  }
}
