package service

import "github.com/tidwall/gjson"

// Compatible usage is preserved from the native provider by the documented
// formatter. Prefer explicit output seconds to aggregate input+output seconds.
func extractCompatibleVideoResponseUsage(body []byte) *MediaUsage {
	usage := gjson.GetBytes(body, "usage")
	u := extractMediaDimensions(usage)
	if resolution := usage.Get("SR"); u.Resolution == "" && resolution.Type == gjson.String {
		u.Resolution = resolution.String()
	}
	for _, key := range []string{"output_video_duration", "output_seconds"} {
		if seconds := mediaPositiveNumber(usage.Get(key)); seconds > 0 {
			u.DurationSeconds = seconds
			return u
		}
	}
	if total := mediaPositiveNumber(usage.Get("total_seconds")); total > 0 {
		input := mediaPositiveNumber(usage.Get("input_seconds"))
		if total > input {
			u.DurationSeconds = total - input
		}
	}
	return u
}
