package service

import (
	"math"
	"strings"

	"github.com/tidwall/gjson"
)

// Count actual submitted references, never client-provided usage/counters. A
// complete text-only request proves zero; an opaque draft cannot prove absence.
func extractMediaReferenceRequest(root gjson.Result) (images *int, inputSeconds *float64) {
	count, seconds, known := 0, 0.0, true
	var addImages func(gjson.Result)
	addImages = func(v gjson.Result) {
		if v.IsArray() {
			for _, item := range v.Array() {
				addImages(item)
			}
		} else if mediaInputPresent(v) {
			count++
		}
	}
	var addVideos func(gjson.Result)
	addVideos = func(v gjson.Result) {
		if v.IsArray() {
			for _, item := range v.Array() {
				addVideos(item)
			}
			return
		}
		if !mediaInputPresent(v) {
			return
		}
		// Bare URLs carry no measured duration. Never turn that into zero seconds.
		duration := mediaPositiveNumber(v.Get("duration"))
		if duration == 0 {
			duration = mediaPositiveNumber(v.Get("seconds"))
		}
		if duration == 0 {
			duration = mediaPositiveNumber(v.Get("video_url.duration"))
		}
		if duration == 0 {
			duration = mediaPositiveNumber(v.Get("video_url.seconds"))
		}
		if duration == 0 {
			known = false
		} else {
			seconds += duration
		}
	}
	content := root.Get("content")
	model := strings.ToLower(root.Get("model").String())
	if !content.IsArray() && strings.HasPrefix(model, "kling") {
		content = root.Get("contents")
	}
	if !content.IsArray() && strings.HasPrefix(model, "wan") {
		content = root.Get("input.media")
	}
	opaque := mediaInputPresent(root.Get("draft_task_id"))
	if content.IsArray() {
		for _, item := range content.Array() {
			switch item.Get("type").String() {
			case "image_url", "input_image":
				addImages(item)
			case "first_frame", "last_frame", "middle_frame", "refer_image", "reference_image":
				addImages(item)
			case "video_url", "base_video", "feature_video", "reference_video", "video":
				addVideos(item)
			case "draft_task":
				opaque = true
			}
		}
	} else {
		for _, key := range []string{"image", "images", "image_url", "image_urls", "input_reference", "reference_images", "first_frame_image", "last_frame_image", "first_frame", "last_frame"} {
			addImages(root.Get(key))
		}
		for _, key := range []string{"video", "videos", "video_url", "video_urls", "reference_video", "reference_videos"} {
			addVideos(root.Get(key))
		}
	}
	if opaque {
		return nil, nil
	}
	images = &count
	if known && mediaFinite(seconds) {
		inputSeconds = &seconds
	}
	return
}

func isMediaImageFormField(name string) bool {
	// Match the image upload parser, including indexed image[0] fields.
	if strings.HasPrefix(name, "image[") {
		return true
	}
	switch name {
	case "image", "image[]", "images", "images[]", "image_url", "image_urls", "input_reference", "reference_images", "first_frame_image", "last_frame_image":
		return true
	}
	return false
}

func appendMediaFormField(fields map[string]any, name string, value any) {
	name = strings.TrimSuffix(name, "[]")
	if old, ok := fields[name]; ok {
		fields[name] = []any{old, value}
	} else {
		fields[name] = value
	}
}

func mediaNonNegativeNumber(v gjson.Result) *float64 {
	if v.Type != gjson.Number || !mediaFinite(v.Float()) || v.Float() < 0 {
		return nil
	}
	value := v.Float()
	return &value
}

// Only call for upstream responses. Native task.usage overrides compatible
// root usage, including explicit zero. Missing fields retain earlier evidence.
func extractMediaReferenceResponse(root gjson.Result) *MediaUsage {
	out := &MediaUsage{}
	for _, path := range []string{"usage", "response.usage", "task.usage"} {
		usage := root.Get(path)
		for _, key := range []string{"input_images", "input_image_count"} {
			v := usage.Get(key)
			if n := mediaNonNegativeNumber(v); n != nil && *n == math.Trunc(*n) && *n < float64(int(^uint(0)>>1)) {
				value := int(*n)
				out.InputImageCount = &value
			}
		}
		for _, key := range []string{"input_video_duration", "input_seconds"} {
			if n := mediaNonNegativeNumber(usage.Get(key)); n != nil {
				out.InputDurationSeconds = n
			}
		}
		if n := mediaNonNegativeNumber(usage.Get("total_seconds")); n != nil {
			out.TotalDurationSeconds = n
		}
	}
	return out
}
