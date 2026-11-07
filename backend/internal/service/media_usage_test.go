package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestMediaRuntimeRequestAndActualDimensions(t *testing.T) {
	request := ExtractMediaRequestUsage([]byte(`{"model":"seedance","resolution":"4k","duration":-1,"generate_audio":true,"content":[{"type":"video_url","video_url":{"url":"https://example.test/ref.mp4"},"role":"reference_video"}]}`))
	require.Equal(t, "4k", request.Resolution)
	require.Zero(t, request.Count)
	require.Zero(t, request.DurationSeconds, "-1 is provider-selected, not a billable duration")
	require.Equal(t, "video", request.InputType)
	require.True(t, *request.ReferenceVideo)
	require.True(t, *request.Audio)
	actual := extractMediaDimensions(gjson.Parse(`{"resolution":"1080p","duration":11,"framespersecond":24,"frames":264,"generate_audio":false}`))
	actual.Count = 1
	merged := MergeMediaUsage(request, actual)
	require.Equal(t, "1080p", merged.Resolution)
	require.Equal(t, 11.0, merged.DurationSeconds)
	require.Equal(t, 24.0, merged.FPS)
	require.EqualValues(t, 264, merged.Frames)
	require.True(t, *merged.ReferenceVideo)
	require.False(t, *merged.Audio)
	require.Equal(t, 1, merged.Count)
	*merged.ReferenceVideo = false
	require.True(t, *request.ReferenceVideo, "snapshot must not share boolean pointers")
	require.True(t, *request.Audio)
}

func TestMediaRuntimeUnknownIsNotDefaulted(t *testing.T) {
	for _, body := range []string{`{}`, `{"duration":-1,"size":"auto"}`, `{"generate_audio":"false","framespersecond":"24"}`} {
		u := ExtractMediaRequestUsage([]byte(body))
		require.Nil(t, u.Audio)
		require.Nil(t, u.Layer)
		require.Empty(t, u.Resolution)
		require.Zero(t, u.DurationSeconds)
		require.Zero(t, u.FPS)
		require.Zero(t, u.Count)
	}
	native := ExtractMediaRequestUsage([]byte(`{"model":"seedance-2.0","content":[{"type":"text","text":"waves"}],"videos":["https://example.test/ignored.mp4"],"contents":[{"type":"base_video","url":"https://example.test/ignored2.mp4"}]}`))
	require.False(t, *native.ReferenceVideo, "native content overrides ignored compatible fields")
	require.Equal(t, "text", native.InputType)
	require.Nil(t, ExtractMediaRequestUsage([]byte(`[]`)))
	require.Nil(t, ExtractMediaRequestUsage([]byte(`{`)))
	draft := ExtractMediaRequestUsage([]byte(`{"content":[{"type":"draft_task","draft_task":{"id":"opaque"}}]}`))
	require.Nil(t, draft.ReferenceVideo)
	require.Empty(t, draft.InputType)
	size := ExtractMediaRequestUsage([]byte(`{"size":"999999999999x999999999999","seconds":"4"}`))
	require.Zero(t, size.Pixels, "overflow must not wrap into a low pixel price tier")
	require.Equal(t, 4.0, size.DurationSeconds)
}

func TestMediaRuntimeImagesKeepPerOutputGeometryAndLayer(t *testing.T) {
	request := ExtractMediaRequestUsage([]byte(`{"size":"2K","layer_decomposition":false,"image":"https://example.test/input.png","n":8}`))
	counter := newOpenAIImageOutputCounter()
	counter.AddJSONResponse([]byte(`{"data":[{"url":"https://example.test/a","size":"2048x1024","z_index":0},{"error":{"code":"Failed"}},{"url":"https://example.test/b","size":"4096x2048"}]}`))
	u := counter.MediaUsage(request)
	require.Equal(t, 2, u.Count)
	require.Len(t, u.Images, 2)
	require.EqualValues(t, 2097152, u.Images[0].Pixels)
	require.True(t, *u.Images[0].Layer)
	require.EqualValues(t, 8388608, u.Images[1].Pixels)
	require.False(t, *u.Images[1].Layer)
	require.Equal(t, "image", u.Images[1].InputType)
	require.Nil(t, u.Images[0].Images)
	require.Nil(t, u.Images[1].Images)
	raw, err := json.Marshal(u)
	require.NoError(t, err)
	var restored MediaUsage
	require.NoError(t, json.Unmarshal(raw, &restored))
	require.Equal(t, *u, restored)
	failed := newOpenAIImageOutputCounter()
	failed.AddJSONResponse([]byte(`{"data":[{"error":{"code":"Failed"},"url":"https://example.test/not-success"}]}`))
	require.Zero(t, failed.MediaUsage(request).Count)
}
