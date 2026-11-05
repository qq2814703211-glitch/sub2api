import type { MediaPricing } from '@/types/mediaPricing'

/** Synthetic configuration fixture covering every agreed wire field, not a vendor quote. */
export const mediaPricingFixture: MediaPricing = {
  extra_input_image_price: 0.02,
  free_input_images: 1,
  duration_source: 'output',
  rules: [
    {
      name: 'pixel / layer / prompt', unit: 'image', price: 0.12, prompt_price: 0.01,
      conditions: { min_pixels: 0, max_pixels: 4194304, layer: false },
    },
    {
      name: 'video dimensions', unit: 'second', price: 0.2,
      conditions: { resolution: '720p', mode: 'standard', input_type: 'text', audio: false, reference_video: false, min_duration: 0, max_duration: 10, fps: 24, min_fps: 24, max_fps: 60 },
    },
    { name: 'frames', unit: 'frame', price: 0, conditions: { audio: true, reference_video: true, layer: true } },
    { name: 'requests', unit: 'request', price: 0.3, conditions: {} },
    { name: 'multimodal', unit: 'token', input_price: 0.000002, output_price: 0.000008, image_input_price: 0, image_output_price: 0.00001, prompt_price: 0.02 },
  ],
}

export const referenceVideoPricingFixture: MediaPricing = {
  extra_input_image_price: 0.2,
  free_input_images: 5,
  duration_source: 'total',
  rules: [{ name: 'reference video', unit: 'second', price: 0.5, conditions: { resolution: '2k' } }],
}
