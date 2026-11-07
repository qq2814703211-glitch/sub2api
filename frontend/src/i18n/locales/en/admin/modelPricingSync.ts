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
    priceUnitHint: 'Prices are shown in USD / 1M tokens (the overlay stores USD / token; converted here).'
  }
}
