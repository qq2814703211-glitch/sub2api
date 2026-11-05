package service

import "github.com/tidwall/gjson"

// Keep pure output duration separate from aggregate input+output seconds.
// Native task.usage is accepted when a relay preserves the native envelope.
func extractCompatibleVideoResponseUsage(body []byte) *MediaUsage {
	root := gjson.ParseBytes(body)
	u := extractMediaReferenceResponse(root)
	for _, path := range []string{"usage", "task.usage"} {
		usage := root.Get(path)
		u = MergeMediaUsage(u, extractMediaDimensions(usage))
		if resolution := usage.Get("SR"); resolution.Type == gjson.String {
			u.Resolution = resolution.String()
		}
		for _, key := range []string{"output_video_duration", "output_seconds"} {
			if seconds := mediaPositiveNumber(usage.Get(key)); seconds > 0 {
				u.DurationSeconds = seconds
			}
		}
	}
	if u.DurationSeconds == 0 && u.TotalDurationSeconds != nil && u.InputDurationSeconds != nil && *u.TotalDurationSeconds > *u.InputDurationSeconds {
		u.DurationSeconds = *u.TotalDurationSeconds - *u.InputDurationSeconds
	}
	return u
}
