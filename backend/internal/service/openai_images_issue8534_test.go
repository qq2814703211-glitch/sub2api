package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Exercises real HTTP serialization after parsing an SDK-style multipart upload.
func TestIssue8534MultipartMarkupHTTP(t *testing.T) {
	var pngBody bytes.Buffer
	require.NoError(t, png.Encode(&pngBody, image.NewNRGBA(image.Rect(0, 0, 2, 2))))
	expectedURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBody.Bytes())
	for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(kind, func(t *testing.T) {
			var body bytes.Buffer
			w := multipart.NewWriter(&body)
			for key, value := range map[string]string{"model": "gpt-image-2.6", "prompt": "use the annotation", "quality": "xhigh", "complexity": "high"} {
				require.NoError(t, w.WriteField(key, value))
			}
			for _, name := range []string{"image[]", "markup"} {
				part, err := w.CreatePart(textproto.MIMEHeader{"Content-Disposition": {fmt.Sprintf(`form-data; name=%q; filename="image.png"`, name)}, "Content-Type": {"image/png"}})
				require.NoError(t, err)
				_, err = part.Write(pngBody.Bytes())
				require.NoError(t, err)
			}
			require.NoError(t, w.Close())
			type captured struct {
				path, contentType, authorization string
				body                             []byte
			}
			requests := make(chan captured, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				requests <- captured{r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("Authorization"), b}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[{"b64_json":"AA==","output_format":"png"}],"usage":{"input_tokens":19,"output_tokens":439}}`))
			}))
			defer server.Close()
			upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				destination, err := url.Parse(server.URL)
				if err != nil {
					return nil, err
				}
				forwarded := req.Clone(req.Context())
				target := *req.URL
				target.Scheme = destination.Scheme
				target.Host = destination.Host
				forwarded.URL = &target
				forwarded.Host = destination.Host
				return server.Client().Do(forwarded)
			}}
			c, rec := newOpenAIImagesTestContext(t, body.Bytes())
			c.Request.URL.Path = "/v1/images/edits"
			c.Request.Header.Set("Content-Type", w.FormDataContentType())
			svc := newOpenAIImagesTestService(upstream)
			parsed, err := svc.ParseOpenAIImagesRequest(c, body.Bytes())
			require.NoError(t, err)
			account := directImagesTestAccount()
			account.Type = kind
			account.Credentials["model_mapping"] = map[string]any{"gpt-image-2.6": "gpt-image-2.6-flare"}
			result, err := svc.ForwardImages(context.Background(), c, account, body.Bytes(), parsed, "")
			require.NoError(t, err)
			got := <-requests
			require.Equal(t, "/backend-api/codex/images/edits", got.path)
			require.Equal(t, "application/json", got.contentType)
			require.Equal(t, "Bearer test-token", got.authorization)
			require.Equal(t, "gpt-image-2.6-flare", gjson.GetBytes(got.body, "model").String())
			require.Equal(t, expectedURL, gjson.GetBytes(got.body, "markup.image_url").String())
			require.Equal(t, expectedURL, gjson.GetBytes(got.body, "images.0.image_url").String())
			require.Len(t, gjson.GetBytes(got.body, "images").Array(), 1)
			require.Equal(t, "xhigh", gjson.GetBytes(got.body, "quality").String())
			require.Equal(t, "high", gjson.GetBytes(got.body, "complexity").String())
			require.False(t, gjson.GetBytes(got.body, "tools").Exists())
			require.False(t, gjson.GetBytes(got.body, "reasoning").Exists())
			require.Equal(t, 200, rec.Code)
			require.Equal(t, 1, result.ImageCount)
		})
	}
}

func TestIssue8534MultipartSVGOptions(t *testing.T) {
	for _, brackets := range []bool{false, true} {
		t.Run(fmt.Sprint(brackets), func(t *testing.T) {
			modelKey, effortKey := "svg.model", "svg.reasoning.effort"
			if brackets {
				modelKey, effortKey = "svg[model]", "svg[reasoning][effort]"
			}
			var body bytes.Buffer
			w := multipart.NewWriter(&body)
			for key, value := range map[string]string{"model": "gpt-image-2.6-sunburst", "prompt": "vector", "output_format": "svg", modelKey: "svg-author", effortKey: "max"} {
				require.NoError(t, w.WriteField(key, value))
			}
			require.NoError(t, w.Close())
			c, _ := newOpenAIImagesTestContext(t, body.Bytes())
			c.Request.Header.Set("Content-Type", w.FormDataContentType())
			upstream := &httpUpstreamRecorder{resp: openAIImagesJSONResponse()}
			svc := newOpenAIImagesTestService(upstream)
			parsed, err := svc.ParseOpenAIImagesRequest(c, body.Bytes())
			require.NoError(t, err)
			_, err = svc.ForwardImages(context.Background(), c, directImagesTestAccount(), body.Bytes(), parsed, "")
			require.NoError(t, err)
			require.Equal(t, "svg", gjson.GetBytes(upstream.lastBody, "output_format").String())
			require.Equal(t, "svg-author", gjson.GetBytes(upstream.lastBody, "svg.model").String())
			require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "svg.reasoning.effort").String())
		})
	}
}

func TestIssue8534StreamingFormats(t *testing.T) {
	for _, format := range []string{"svg", "gif"} {
		for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
			t.Run(format+"/"+kind, func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"model":"gpt-image-2.6-flare","prompt":"draw","output_format":%q,"response_format":"url","stream":true}`, format))
				response := fmt.Sprintf("data: {\"type\":\"image_generation.completed\",\"b64_json\":\"AA==\",\"output_format\":%q,\"usage\":{\"output_tokens\":12}}\n\n", format)
				c, rec := newOpenAIImagesTestContext(t, body)
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(response))}}
				svc := newOpenAIImagesTestService(upstream)
				parsed, err := svc.ParseOpenAIImagesRequest(c, body)
				require.NoError(t, err)
				account := directImagesTestAccount()
				account.Type = kind
				result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
				require.NoError(t, err)
				mime := map[string]string{"svg": "image/svg+xml", "gif": "image/gif"}[format]
				require.Contains(t, rec.Body.String(), "data:"+mime+";base64,AA==")
				require.Equal(t, "/backend-api/codex/images/generations", upstream.lastReq.URL.Path)
				require.Equal(t, format, gjson.GetBytes(upstream.lastBody, "output_format").String())
				require.Equal(t, 1, result.ImageCount)
				require.Equal(t, 12, result.Usage.ImageOutputTokens)
			})
		}
	}
}

func TestIssue8534UserErrorStatus(t *testing.T) {
	for _, errorType := range []string{"invalid_value", "image_generation_user_error"} {
		t.Run(errorType, func(t *testing.T) {
			body := []byte(`{"model":"gpt-image-2.6-flare","prompt":"loop","output_format":"gif"}`)
			c, rec := newOpenAIImagesTestContext(t, body)
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"error":{"type":%q,"message":"request unsupported"}}`, errorType)))}}
			svc := newOpenAIImagesTestService(upstream)
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			result, err := svc.ForwardImages(context.Background(), c, directImagesTestAccount(), body, parsed, "")
			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, 400, rec.Code)
			require.Contains(t, rec.Body.String(), "request unsupported")
			require.Len(t, upstream.bodies, 1)
		})
	}
}

func TestIssue8534FrameIntegers(t *testing.T) {
	for _, field := range []string{"frame_count", "frame_duration_ms"} {
		for _, value := range []string{"4.5", "4.0000000000000001", "1e100", "9223372036854775808", "-1", `"4"`, "true"} {
			t.Run(field+"/"+value, func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"model":"gpt-image-2.6-flare","prompt":"loop",%q:%s}`, field, value))
				c, _ := newOpenAIImagesTestContext(t, body)
				_, err := newOpenAIImagesTestService(nil).ParseOpenAIImagesRequest(c, body)
				require.Error(t, err)
			})
		}
		for _, value := range []string{"4", "4.0", "4e0"} {
			body := []byte(fmt.Sprintf(`{"model":"gpt-image-2.6-flare","prompt":"loop",%q:%s}`, field, value))
			c, _ := newOpenAIImagesTestContext(t, body)
			parsed, err := newOpenAIImagesTestService(nil).ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			payload, _, err := buildOpenAIImagesOAuthPayload(parsed, parsed.Model)
			require.NoError(t, err)
			require.EqualValues(t, 4, gjson.GetBytes(payload, field).Int())
		}
	}
}

func TestIssue8534OtherProvidersKeepFormatOptions(t *testing.T) {
	for _, format := range []string{"svg", "gif"} {
		body := []byte(fmt.Sprintf(`{"model":"grok-imagine-image","prompt":"edit","images":[{"image_url":"https://example.com/base.png"}],"output_format":%q,"stream":true,"partial_images":1,"output_compression":90}`, format))
		c, _ := newOpenAIImagesTestContext(t, body)
		c.Request.URL.Path = "/v1/images/edits"
		_, err := newOpenAIImagesTestService(nil).ParseOpenAIImagesRequest(c, body)
		require.NoError(t, err)
	}
}

func TestIssue8534MarkupJSONForwarding(t *testing.T) {
	for _, reference := range []string{`{"image_url":"data:image/png;base64,AA=="}`, `{"file_id":"file-markup"}`} {
		for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
			t.Run(kind+"/"+reference, func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"model":"gpt-image-2.6","prompt":"edit","quality":"max","complexity":"high","images":[{"image_url":"https://example.com/base.png"}],"markup":%s}`, reference))
				c, _ := newOpenAIImagesTestContext(t, body)
				c.Request.URL.Path = "/v1/images/edits"
				upstream := &httpUpstreamRecorder{resp: openAIImagesJSONResponse()}
				svc := newOpenAIImagesTestService(upstream)
				parsed, err := svc.ParseOpenAIImagesRequest(c, body)
				require.NoError(t, err)
				account := directImagesTestAccount()
				account.Type = kind
				account.Credentials["model_mapping"] = map[string]any{"gpt-image-2.6": "gpt-image-2.6-sunburst"}
				result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
				require.NoError(t, err)
				require.Equal(t, "gpt-image-2.6-sunburst", result.UpstreamModel)
				require.Equal(t, "/backend-api/codex/images/edits", upstream.lastReq.URL.Path)
				require.JSONEq(t, reference, gjson.GetBytes(upstream.lastBody, "markup").Raw)
				require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "quality").String())
				require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "complexity").String())
				require.False(t, gjson.GetBytes(upstream.lastBody, "reasoning").Exists())
			})
		}
	}
}

func TestIssue8534NoResponsesFallback(t *testing.T) {
	for _, model := range []string{"gpt-image-2.6", "gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		for _, status := range []int{404, 405} {
			t.Run(fmt.Sprintf("%s/%d", model, status), func(t *testing.T) {
				calls := 0
				upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
					calls++
					require.Equal(t, "/backend-api/codex/images/generations", req.URL.Path)
					return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"endpoint unavailable"}}`))}, nil
				}}
				body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"draw"}`, model))
				c, _ := newOpenAIImagesTestContext(t, body)
				svc := newOpenAIImagesTestService(upstream)
				parsed, err := svc.ParseOpenAIImagesRequest(c, body)
				require.NoError(t, err)
				_, err = svc.ForwardImages(context.Background(), c, directImagesTestAccount(), body, parsed, "")
				require.Error(t, err)
				require.Equal(t, 1, calls)
			})
		}
	}
}
