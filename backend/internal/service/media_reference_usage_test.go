//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMediaReferenceRequestCountIgnoresClientUsage(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		count      *int
		input      *float64
	}{
		{"text known zero", `{"prompt":"x","usage":{"input_images":9,"input_seconds":88,"total_seconds":99},"input_image_count":42}`, mediaTestPtr(0), mediaTestPtr(0.0)},
		{"image arrays and frames", `{"images":["a",{"url":"b","role":"first_frame"},{"url":"c","role":"last_frame"}],"reference_images":["d"],"first_frame_image":"e","last_frame_image":"f","usage":{"input_images":0}}`, mediaTestPtr(6), mediaTestPtr(0.0)},
		{"native content authoritative", `{"images":["ignored"],"content":[{"type":"image_url","image_url":{"url":"a"},"role":"first_frame"},{"type":"image_url","image_url":{"url":"b"},"role":"last_frame"},{"type":"image_url","image_url":{"url":"c"},"role":"reference_image"}]}`, mediaTestPtr(3), mediaTestPtr(0.0)},
		{"exact reference video seconds", `{"videos":[{"url":"a","duration":1.5},{"url":"b","duration":2.5}]}`, mediaTestPtr(0), mediaTestPtr(4.0)},
		{"video unknown seconds", `{"videos":[{"url":"a","duration":1.5},"b"],"usage":{"input_seconds":0}}`, mediaTestPtr(0), nil},
		{"native video unknown", `{"content":[{"type":"video_url","video_url":{"url":"a"}}]}`, mediaTestPtr(0), nil},
		{"native video exact", `{"content":[{"type":"video_url","video_url":{"url":"a","duration":2.5}}]}`, mediaTestPtr(0), mediaTestPtr(2.5)},
		{"opaque draft", `{"content":[{"type":"draft_task","draft_task":{"id":"opaque"}}]}`, nil, nil},
		{"kling native", `{"model":"kling-v3","contents":[{"type":"first_frame","url":"a"},{"type":"refer_image","url":"b"},{"type":"base_video","url":"v","duration":3}]}`, mediaTestPtr(2), mediaTestPtr(3.0)},
		{"wan native", `{"model":"wan","input":{"media":[{"type":"reference_image","url":"a"}]}}`, mediaTestPtr(1), mediaTestPtr(0.0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := ExtractMediaRequestUsage([]byte(tc.body))
			require.NotNil(t, u)
			require.Equal(t, tc.count, u.InputImageCount)
			require.Equal(t, tc.input, u.InputDurationSeconds)
			require.Nil(t, u.TotalDurationSeconds)
		})
	}
}

func TestMediaReferenceMultipartCountsFilesAndFields(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, name := range []string{"image", "image[]", "image[0]", "input_reference", "mask"} {
		part, err := w.CreateFormFile(name, "fixture.png")
		require.NoError(t, err)
		_, err = part.Write([]byte("fixture"))
		require.NoError(t, err)
	}
	require.NoError(t, w.WriteField("image_url", "a"))
	require.NoError(t, w.WriteField("image_url", "b"))
	require.NoError(t, w.WriteField("usage", `{"input_images":0}`))
	require.NoError(t, w.Close())
	u := extractMediaRequestUsageBody(w.FormDataContentType(), body.Bytes())
	require.NotNil(t, u)
	require.Equal(t, mediaTestPtr(6), u.InputImageCount)
}

func TestMediaReferenceResponseOverridesAndSnapshotIsolation(t *testing.T) {
	request := ExtractMediaRequestUsage([]byte(`{"images":["a","b"],"videos":[{"url":"v","duration":3}]}`))
	response := extractCompatibleVideoResponseUsage([]byte(`{"usage":{"input_images":9,"total_seconds":10},"task":{"usage":{"input_image_count":0,"input_seconds":0,"output_seconds":4,"total_seconds":4}}}`))
	merged := MergeMediaUsage(request, response)
	require.Equal(t, mediaTestPtr(0), merged.InputImageCount)
	require.Equal(t, mediaTestPtr(0.0), merged.InputDurationSeconds)
	require.Equal(t, mediaTestPtr(4.0), merged.TotalDurationSeconds)
	require.Equal(t, 4.0, merged.DurationSeconds)
	snapshot := GrokVideoPendingBilling{MediaUsage: merged}
	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var restored GrokVideoPendingBilling
	require.NoError(t, json.Unmarshal(raw, &restored))
	require.Equal(t, merged, restored.MediaUsage)
	*merged.InputImageCount = 7
	*merged.InputDurationSeconds = 8
	*merged.TotalDurationSeconds = 9
	require.Equal(t, mediaTestPtr(2), request.InputImageCount)
	require.Equal(t, mediaTestPtr(0), response.InputImageCount)
	require.Equal(t, mediaTestPtr(0), restored.MediaUsage.InputImageCount)
	cloned := MergeMediaUsage(response, nil)
	*cloned.InputImageCount = 11
	*cloned.InputDurationSeconds = 12
	*cloned.TotalDurationSeconds = 13
	require.Equal(t, mediaTestPtr(0), response.InputImageCount)
	require.Equal(t, mediaTestPtr(0.0), response.InputDurationSeconds)
	require.Equal(t, mediaTestPtr(4.0), response.TotalDurationSeconds)
}

func TestMediaReferenceTotalWithoutInputDoesNotInventOutput(t *testing.T) {
	u := extractCompatibleVideoResponseUsage([]byte(`{"usage":{"total_seconds":7}}`))
	require.Equal(t, mediaTestPtr(7.0), u.TotalDurationSeconds)
	require.Nil(t, u.InputDurationSeconds)
	require.Zero(t, u.DurationSeconds)
}

func TestMediaReferenceMultipartDoesNotGuessTruncatedArray(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	require.NoError(t, w.WriteField("images", `["`+strings.Repeat("x", 4200)+`","b"]`))
	require.NoError(t, w.Close())
	require.Nil(t, extractMediaRequestUsageBody(w.FormDataContentType(), body.Bytes()))
}

func TestMediaReferenceStreamExplicitZeroIsAuthoritative(t *testing.T) {
	request := ExtractMediaRequestUsage([]byte(`{"images":["a","b","c"],"usage":{"input_images":0}}`))
	counter := newOpenAIImageOutputCounter()
	counter.AddSSEData([]byte(`{"type":"image_generation.partial_succeeded","image_index":0,"url":"https://example.test/out"}`))
	counter.AddSSEData([]byte(`{"type":"image_generation.completed","usage":{"input_images":0}}`))
	u := counter.MediaUsage(request)
	require.Equal(t, 1, u.Count)
	require.Equal(t, mediaTestPtr(0), u.InputImageCount)
	require.Equal(t, mediaTestPtr(3), request.InputImageCount)
}

func TestMediaReferenceSubmitResponseCountsPersistWithoutBilling(t *testing.T) {
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"task","usage":{"input_image_count":0,"input_seconds":0}}`)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	a := seedanceTestAccount()
	a.Credentials["openai_capabilities"] = []string{"video_generations"}
	c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/video/generations", nil)
	result, err := svc.ForwardCompatibleVideo(context.Background(), c, a, CompatibleVideoEndpointCreate, "", []byte(`{"model":"MiniMax-H3","images":["a","b"],"videos":["v"],"usage":{"input_image_count":99}}`))
	require.NoError(t, err)
	require.Zero(t, result.MediaUsage.Count)
	require.Equal(t, mediaTestPtr(0), result.MediaUsage.InputImageCount)
	require.Equal(t, mediaTestPtr(0.0), result.MediaUsage.InputDurationSeconds)
	raw, err := json.Marshal(GrokVideoPendingBilling{MediaUsage: result.MediaUsage})
	require.NoError(t, err)
	var pending GrokVideoPendingBilling
	require.NoError(t, json.Unmarshal(raw, &pending))
	require.Equal(t, result.MediaUsage, pending.MediaUsage)
}
