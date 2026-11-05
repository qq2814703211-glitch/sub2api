/** Shared wire schema; prices use site billing units, token prices are per token. */
export type MediaPricingUnit = 'image' | 'second' | 'frame' | 'request' | 'token'

export interface MediaPricingConditions {
  resolution?: string
  mode?: string
  input_type?: string
  audio?: boolean | null
  reference_video?: boolean | null
  layer?: boolean | null
  min_pixels?: number | null
  max_pixels?: number | null
  min_duration?: number | null
  max_duration?: number | null
  fps?: number | null
  min_fps?: number | null
  max_fps?: number | null
}

export interface MediaPricingRule {
  name?: string
  unit: MediaPricingUnit
  conditions?: MediaPricingConditions
  price?: number | null
  input_price?: number | null
  output_price?: number | null
  image_input_price?: number | null
  image_output_price?: number | null
  prompt_price?: number | null
}

export interface MediaPricing {
  rules: MediaPricingRule[]
  /** Per-request input allowance; never an output discount. */
  extra_input_image_price?: number | null
  free_input_images?: number | null
  /** Defaults to output. Total includes reference-video seconds once. */
  duration_source?: 'output' | 'total'
}
