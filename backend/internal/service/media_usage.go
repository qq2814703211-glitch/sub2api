package service

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// ExtractMediaRequestUsage reads explicit protocol dimensions, never prices or
// requested output counts. A create request is not a successful output.
// Ark: layer_decomposition, content[].{type,role}, generate_audio, framespersecond.
// OpenAI-compatible Images/video and xAI: size, seconds/duration, image(s), video.
func ExtractMediaRequestUsage(body []byte) *MediaUsage {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return nil
	}
	root := gjson.ParseBytes(body)
	usage := extractMediaDimensions(root)
	model := strings.ToLower(root.Get("model").String())
	if strings.HasPrefix(model, "kling") {
		usage = MergeMediaUsage(usage, extractMediaDimensions(root.Get("settings")))
		if audio := root.Get("settings.audio"); audio.Type == gjson.String {
			switch audio.String() {
			case "off":
				v := false
				usage.Audio = &v
			case "native":
				v := true
				usage.Audio = &v
			}
		}
	}
	if strings.HasPrefix(model, "vidu") && usage.Audio == nil {
		usage.Audio = mediaBool(root.Get("audio"))
	}
	if strings.HasPrefix(model, "wan") {
		usage = MergeMediaUsage(usage, extractMediaDimensions(root.Get("parameters")))
		if audio := mediaBool(root.Get("parameters.audio")); audio != nil {
			usage.Audio = audio
		}
	}
	// Only a complete request can establish absence of a reference video. A
	// partial status response must never turn an omitted field into false.
	reference := false
	usage.ReferenceVideo = &reference
	usage.InputType = "text"
	// Native content is authoritative when present; compatible adapters do
	// not overlay top-level media onto an explicit content array.
	if !root.Get("content").IsArray() {
		for _, key := range []string{"image", "images", "image_url", "image_urls", "input_reference", "first_frame_image", "last_frame_image", "first_frame", "last_frame"} {
			if mediaInputPresent(root.Get(key)) {
				usage.InputType = "image"
			}
		}
		if mediaInputPresent(root.Get("reference_images")) {
			usage.InputType = "reference"
		}
		for _, key := range []string{"video", "video_url", "videos", "video_urls", "reference_video", "reference_videos"} {
			if mediaInputPresent(root.Get(key)) {
				reference = true
				usage.InputType = "video"
			}
		}
	}
	if items := root.Get("content"); items.IsArray() {
		for _, item := range items.Array() {
			switch item.Get("type").String() {
			case "image_url":
				if mediaInputPresent(item.Get("image_url")) && usage.InputType == "text" {
					usage.InputType = "image"
				}
				if item.Get("role").String() == "reference_image" {
					usage.InputType = "reference"
				}
			case "video_url":
				if mediaInputPresent(item.Get("video_url")) {
					reference = true
					usage.InputType = "video"
				}
			case "draft_task":
				// A draft can carry inputs not repeated in this request. Do not infer
				// without-reference or text-only pricing from its opaque task ID.
				usage.ReferenceVideo = nil
				usage.InputType = ""
			}
		}
	}
	// Kling native contents and Wan input.media have documented role types.
	nativeMediaPath := ""
	if strings.HasPrefix(model, "kling") {
		nativeMediaPath = "contents"
	}
	if strings.HasPrefix(model, "wan") {
		nativeMediaPath = "input.media"
	}
	if nativeMediaPath != "" {
		for _, path := range []string{nativeMediaPath} {
			for _, item := range root.Get(path).Array() {
				if !mediaInputPresent(item) {
					continue
				}
				switch item.Get("type").String() {
				case "first_frame", "last_frame", "middle_frame":
					if usage.InputType == "text" {
						usage.InputType = "image"
					}
				case "refer_image", "reference_image":
					usage.InputType = "reference"
				case "base_video", "feature_video", "reference_video", "video":
					reference = true
				}
			}
		}
	}
	if items := root.Get("images"); items.IsArray() && !root.Get("content").IsArray() {
		for _, item := range items.Array() {
			if mediaInputPresent(item) && (item.Get("role").String() == "reference_image" || item.Get("type").String() == "reference_image") {
				usage.InputType = "reference"
			}
		}
	}
	if mediaInputPresent(root.Get("draft_task_id")) {
		usage.ReferenceVideo = nil
		usage.InputType = ""
	}
	if reference {
		usage.ReferenceVideo = &reference
		usage.InputType = "video"
	}
	if strings.HasPrefix(model, "vidu") {
		if mediaInputPresent(root.Get("audios")) || mediaInputPresent(root.Get("sounds")) || len(root.Get("images").Array()) > 2 || reference {
			usage.InputType = "reference"
		}
	}
	if isSeedreamCompatibleImageModel(model) && !root.Get("layer_decomposition").Exists() {
		// The documented layer_decomposition default is false. No other
		// provider gets an inferred layer/audio default.
		v := false
		usage.Layer = &v
	}
	ApplyMediaUsageModelDimensions(usage, model)
	usage.InputImageCount, usage.InputDurationSeconds = extractMediaReferenceRequest(root)
	return usage
}

// ApplyMediaUsageModelDimensions derives only documented billing selectors,
// without rewriting the upstream payload. Unknown resolutions remain unknown.
func ApplyMediaUsageModelDimensions(u *MediaUsage, model string) {
	if u == nil || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "kling") {
		return
	}
	switch strings.ToLower(u.Resolution) {
	case "480p", "720p":
		u.Mode = "std"
	case "1080p":
		u.Mode = "pro"
	case "2k", "4k":
		u.Mode = strings.ToLower(u.Resolution)
	}
}

func mediaInputPresent(v gjson.Result) bool {
	if v.Type == gjson.String {
		return strings.TrimSpace(v.String()) != ""
	}
	if v.IsArray() {
		for _, item := range v.Array() {
			if mediaInputPresent(item) {
				return true
			}
		}
	}
	if v.IsObject() {
		for _, key := range []string{"url", "image_url", "video_url", "file_id"} {
			if mediaInputPresent(v.Get(key)) {
				return true
			}
		}
	}
	return false
}

// Multipart metadata is read from fields only; file bytes are never copied into
// the billing snapshot. JSON and multipart requests share the same dimension parser.
func extractMediaRequestUsageBody(contentType string, body []byte) *MediaUsage {
	typ, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(typ, "multipart/form-data") {
		return ExtractMediaRequestUsage(body)
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	fields := make(map[string]any)
	imageFiles := 0
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil
		}
		name := part.FormName()
		if part.FileName() != "" {
			if isMediaImageFormField(name) {
				imageFiles++
			}
			_ = part.Close()
			continue
		}
		switch name {
		case "model", "size", "resolution", "mode", "duration", "seconds", "fps", "framespersecond", "frames", "generate_audio", "layer_decomposition", "image", "image[]", "images", "images[]", "image_url", "image_urls", "input_reference", "reference_images", "first_frame_image", "last_frame_image", "video", "video_url":
			value, err := io.ReadAll(io.LimitReader(part, 4097))
			if err != nil {
				_ = part.Close()
				return nil
			}
			if len(value) > 4096 {
				// A truncated structured media field has an unknown count. Do not
				// collapse an arbitrarily long reference array into a single image.
				prefix := strings.TrimSpace(string(value))
				if strings.HasPrefix(prefix, "[") || strings.HasPrefix(prefix, "{") {
					_ = part.Close()
					return nil
				}
				switch name {
				case "image", "image[]", "images", "images[]", "image_url", "input_reference", "first_frame_image", "last_frame_image", "video", "video_url":
					appendMediaFormField(fields, name, "present")
					_ = part.Close()
					continue
				default:
					_ = part.Close()
					return nil
				}
			}
			text := strings.TrimSpace(string(value))
			switch name {
			case "duration", "fps", "framespersecond", "frames", "generate_audio", "layer_decomposition":
				var value any
				if json.Unmarshal([]byte(text), &value) == nil {
					fields[name] = value
				}
			default:
				if isMediaImageFormField(name) {
					var value any
					if json.Unmarshal([]byte(text), &value) == nil {
						appendMediaFormField(fields, name, value)
					} else {
						appendMediaFormField(fields, name, text)
					}
				} else {
					fields[name] = text
				}
			}
		}
		_ = part.Close()
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil
	}
	usage := ExtractMediaRequestUsage(raw)
	if usage.InputImageCount != nil {
		*usage.InputImageCount += imageFiles
	}
	if usage.InputImageCount != nil && *usage.InputImageCount > 0 && usage.InputType == "text" {
		usage.InputType = "image"
	}
	return usage
}

func extractMediaDimensions(root gjson.Result) *MediaUsage {
	u := &MediaUsage{}
	if v := root.Get("resolution"); v.Type == gjson.String {
		u.Resolution = strings.TrimSpace(v.String())
	}
	if v := root.Get("size"); v.Type == gjson.String {
		setMediaSize(u, v.String())
	}
	if v := root.Get("mode"); v.Type == gjson.String {
		u.Mode = strings.TrimSpace(v.String())
	}
	u.Audio = mediaBool(root.Get("generate_audio"))
	u.Layer = mediaBool(root.Get("layer_decomposition"))
	u.DurationSeconds = mediaPositiveNumber(root.Get("duration"))
	if u.DurationSeconds == 0 {
		seconds := root.Get("seconds")
		if seconds.Type == gjson.String {
			if n, err := strconv.ParseFloat(seconds.String(), 64); err == nil && n > 0 && !math.IsInf(n, 0) && !math.IsNaN(n) {
				u.DurationSeconds = n
			}
		} else {
			u.DurationSeconds = mediaPositiveNumber(seconds)
		}
	}
	u.FPS = mediaPositiveNumber(root.Get("framespersecond"))
	if u.FPS == 0 {
		u.FPS = mediaPositiveNumber(root.Get("fps"))
	}
	if v := root.Get("frames"); v.Type == gjson.Number && v.Int() > 0 && v.Float() == float64(v.Int()) {
		u.Frames = v.Int()
	}
	return u
}

func mediaResponseHasError(root gjson.Result) bool {
	v := root.Get("error")
	if v.Type == gjson.String {
		return strings.TrimSpace(v.String()) != ""
	}
	return v.IsObject()
}

func mediaBool(v gjson.Result) *bool {
	if v.Type != gjson.True && v.Type != gjson.False {
		return nil
	}
	value := v.Bool()
	return &value
}

func mediaPositiveNumber(v gjson.Result) float64 {
	if v.Type != gjson.Number {
		return 0
	}
	value := v.Float()
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func setMediaSize(u *MediaUsage, size string) {
	size = strings.TrimSpace(size)
	if size == "" || strings.EqualFold(size, "auto") || strings.EqualFold(size, "adaptive") {
		return
	}
	u.Resolution = size
	u.Width, u.Height, u.Pixels = 0, 0, 0
	dims := strings.Split(strings.ToLower(size), "x")
	if len(dims) != 2 {
		return
	}
	width, e1 := strconv.ParseInt(strings.TrimSpace(dims[0]), 10, 64)
	height, e2 := strconv.ParseInt(strings.TrimSpace(dims[1]), 10, 64)
	if e1 != nil || e2 != nil || width <= 0 || height <= 0 || width > math.MaxInt64/height {
		return
	}
	u.Width, u.Height, u.Pixels = width, height, width*height
}

// MergeMediaUsage returns an independent snapshot. Actual output dimensions
// override requested dimensions. Absent booleans preserve request values; an
// explicit false does override true. Count always comes from the response.
func MergeMediaUsage(request, response *MediaUsage) *MediaUsage {
	if request == nil && response == nil {
		return nil
	}
	out := cloneMediaUsage(request)
	if out == nil {
		out = &MediaUsage{}
	}
	if response == nil {
		return out
	}
	if response.Resolution != "" {
		out.Resolution = response.Resolution
		out.Width, out.Height, out.Pixels = response.Width, response.Height, response.Pixels
	}
	if response.Width > 0 {
		out.Width = response.Width
	}
	if response.Height > 0 {
		out.Height = response.Height
	}
	if response.Pixels > 0 {
		out.Pixels = response.Pixels
	}
	if response.Mode != "" {
		out.Mode = response.Mode
	}
	if response.InputType != "" {
		out.InputType = response.InputType
	}
	if response.Audio != nil {
		v := *response.Audio
		out.Audio = &v
	}
	if response.ReferenceVideo != nil {
		v := *response.ReferenceVideo
		out.ReferenceVideo = &v
	}
	if response.Layer != nil {
		v := *response.Layer
		out.Layer = &v
	}
	if response.DurationSeconds > 0 {
		out.DurationSeconds = response.DurationSeconds
	}
	if response.InputImageCount != nil {
		out.InputImageCount = mediaCopyPtr(response.InputImageCount)
	}
	if response.InputDurationSeconds != nil {
		out.InputDurationSeconds = mediaCopyPtr(response.InputDurationSeconds)
	}
	if response.TotalDurationSeconds != nil {
		out.TotalDurationSeconds = mediaCopyPtr(response.TotalDurationSeconds)
	}
	if response.FPS > 0 {
		out.FPS = response.FPS
	}
	if response.Frames > 0 {
		out.Frames = response.Frames
	}
	out.Count = response.Count
	out.Images = nil
	for i := range response.Images {
		base := *out
		base.Images = nil
		image := MergeMediaUsage(&base, &response.Images[i])
		image.Images = nil
		out.Images = append(out.Images, *image)
	}
	return out
}

func cloneMediaUsage(u *MediaUsage) *MediaUsage {
	if u == nil {
		return nil
	}
	out := *u
	out.InputImageCount = mediaCopyPtr(u.InputImageCount)
	out.InputDurationSeconds = mediaCopyPtr(u.InputDurationSeconds)
	out.TotalDurationSeconds = mediaCopyPtr(u.TotalDurationSeconds)
	if u.Audio != nil {
		v := *u.Audio
		out.Audio = &v
	}
	if u.ReferenceVideo != nil {
		v := *u.ReferenceVideo
		out.ReferenceVideo = &v
	}
	if u.Layer != nil {
		v := *u.Layer
		out.Layer = &v
	}
	out.Images = nil
	for i := range u.Images {
		out.Images = append(out.Images, *cloneMediaUsage(&u.Images[i]))
	}
	return &out
}

func imageOutputMediaUsage(item gjson.Result) MediaUsage {
	u := extractMediaDimensions(item)
	// Ark layer outputs carry z_index (zero is the background layer).
	if v := item.Get("z_index"); v.Type == gjson.Number {
		value := true
		u.Layer = &value
	}
	if u.Pixels == 0 {
		if size := detectOpenAIImageResultSize(firstNonEmpty(item.Get("b64_json").String(), item.Get("result").String())); size != "" {
			setMediaSize(u, size)
		}
	}
	u.Count = 1
	return *u
}

func (c *openAIImageOutputCounter) MediaUsage(request *MediaUsage) *MediaUsage {
	response := cloneMediaUsage(c.referenceUsage)
	if response == nil {
		response = &MediaUsage{}
	}
	response.Count = c.Count()
	if len(c.dataMedia) > len(c.seenOrder) {
		response.Images = c.dataMedia
	} else {
		for _, key := range c.seenOrder {
			response.Images = append(response.Images, c.seenMedia[key])
		}
	}
	return MergeMediaUsage(request, response)
}
