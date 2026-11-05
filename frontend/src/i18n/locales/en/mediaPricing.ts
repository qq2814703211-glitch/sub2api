export default {
  "conditions": {
    "resolution": "Resolution",
    "mode": "Mode",
    "input_type": "Input type",
    "audio": "Audio",
    "reference_video": "Reference video",
    "layer": "Layer",
    "min_pixels": "Min pixels (inclusive)",
    "max_pixels": "Max pixels (inclusive)",
    "min_duration": "Min duration (seconds, inclusive)",
    "max_duration": "Max duration (seconds, inclusive)",
    "fps": "FPS",
    "min_fps": "Min FPS (inclusive)",
    "max_fps": "Max FPS (inclusive)"
  },
  "prices": {
    "input_price": "Text input",
    "output_price": "Text output",
    "image_input_price": "Image input",
    "image_output_price": "Image output",
    "prompt_price": "Prompt surcharge"
  },
  "units": {
    "image": "image",
    "second": "second",
    "frame": "frame",
    "request": "request",
    "token": "token"
  },
  "yes": "Yes",
  "no": "No",
  "editorTitle": "Multidimensional media pricing (optional JSON)",
  "empty": "Leave blank or enter null to use existing pricing",
  "editorHint": "Prices use site billing units without currency conversion. For image/second/frame/request, price is per image/second/frame/request. Token input_price, output_price, image_input_price and image_output_price are per token (not per million). prompt_price is a once-per-request text surcharge. Explicit false and 0 are retained.",
  "matchHint": "Conditions: resolution, mode, input_type, audio, reference_video, layer, min_pixels/max_pixels, min_duration/max_duration, fps/min_fps/max_fps. Pixel, duration and FPS bounds are inclusive; the next pixel tier starts at the previous maximum + 1. More conditions take priority, then declaration order. Missing match data or rules produces an error, not a scalar-price fallback. Configure explicit, non-overlapping tiers.",
  "examples": "View rule examples",
  "exampleHint": "Configuration examples only, not vendor quotes. Copy the structure you need and enter actual rules. Blank or null clears rules and restores existing pricing.",
  "exampleLabels": {
    "image": "Image: pixels / layers / prompt",
    "video": "Video: seconds / frames / resolution / audio / reference / FPS / duration",
    "token": "Multimodal tokens"
  },
  "badge": "Media rules",
  "unitsPerRule": "(units shown per rule)",
  "mixedUnits": "(token prices per million; media units per rule)",
  "displayTitle": "Conditional pricing (not a single price)",
  "displayHint": "Site billing units below; conditions determine the price, bounds are inclusive, and missing match data or rules produces an error.",
  "allConditions": "Any conditions",
  "unnamedRule": "Rule {index}",
  "validation": {
    "json": "Invalid media pricing JSON. Correct it before saving.",
    "rules": "Media pricing must be an object with a non-empty rules array; leave blank or enter null to use existing pricing.",
    "rule": "{field} must be a rule object.",
    "unknown": "{field} is unknown. Check the field name.",
    "string": "{field} must be text.",
    "unit": "{field} must be image, second, frame, request or token.",
    "price": "{field} must be a finite non-negative number.",
    "unitPrices": "{field}: use price for image/second/frame/request and token price fields only for token rules.",
    "tokenPrice": "{field}: a token rule needs at least one token price.",
    "directPrice": "{field} must specify a per-unit price (0 is allowed).",
    "conditions": "{field} must be a conditions object.",
    "boolean": "{field} must be true, false or null.",
    "number": "{field} must be a finite non-negative number; pixel counts must be safe integers.",
    "range": "{field}: minimum cannot exceed maximum, and exact FPS must be within the range."
  }
}
