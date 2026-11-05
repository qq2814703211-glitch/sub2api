//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Contract pins only: existing API-key passthrough, not proof of upstream
// feature availability or OAuth/Responses support for Images-only features.
func TestGPT26APIKeyExtendedImageFieldsPassThrough(t *testing.T) {
	gpt26RestoreGinMode(t)
	for _, format := range []string{"svg", "gif"} {
		t.Run(format, func(t *testing.T) {
			extra := `"svg":{"model":"gpt-5.5","reasoning":{"effort":"medium"}}`
			if format == "gif" {
				extra = `"frame_count":4,"frame_duration_ms":200`
			}
			body := []byte(fmt.Sprintf(`{"model":"gpt-image-2.6-flare","prompt":"draw","output_format":%q,"complexity":"high",%s}`, format, extra))
			response := fmt.Sprintf(`{"data":[{"b64_json":"AA==","output_format":%q}],"usage":{"input_tokens":19,"output_tokens":439}}`, format)
			upstream := &httpUpstreamRecorder{resp: gpt26Response(response, "application/json")}
			svc := newOpenAIImagesTestService(upstream)
			c, rec := newOpenAIImagesTestContext(t, body)
			account := directImagesTestAccount()
			account.Type = AccountTypeAPIKey
			account.Credentials["api_key"] = "sk-test"
			account.Credentials["base_url"] = "https://api.openai.com/v1"
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
			require.NoError(t, err)
			require.Equal(t, body, upstream.lastBody)
			require.Equal(t, "/v1/images/generations", upstream.lastReq.URL.Path)
			require.Equal(t, format, gjson.GetBytes(rec.Body.Bytes(), "data.0.output_format").String())
			require.Equal(t, 1, result.ImageCount)
		})
	}
	t.Run("markup_multipart", func(t *testing.T) {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		require.NoError(t, writer.WriteField("model", "gpt-image-2.6-flare"))
		require.NoError(t, writer.WriteField("prompt", "apply the marked edit"))
		for _, name := range []string{"image", "markup"} {
			p, err := writer.CreateFormFile(name, name+".png")
			require.NoError(t, err)
			_, err = p.Write([]byte(name + "-bytes"))
			require.NoError(t, err)
		}
		require.NoError(t, writer.Close())
		upstream := &httpUpstreamRecorder{resp: gpt26Response(`{"data":[{"b64_json":"AA=="}],"usage":{"input_tokens":19,"output_tokens":439}}`, "application/json")}
		svc := newOpenAIImagesTestService(upstream)
		c, _ := newOpenAIImagesTestContext(t, buf.Bytes())
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		c.Request.URL.Path = "/v1/images/edits"
		account := directImagesTestAccount()
		account.Type = AccountTypeAPIKey
		account.Credentials["api_key"] = "sk-test"
		account.Credentials["base_url"] = "https://api.openai.com/v1"
		parsed, err := svc.ParseOpenAIImagesRequest(c, buf.Bytes())
		require.NoError(t, err)
		_, err = svc.ForwardImages(context.Background(), c, account, buf.Bytes(), parsed, "")
		require.NoError(t, err)
		_, params, err := mime.ParseMediaType(upstream.lastReq.Header.Get("Content-Type"))
		require.NoError(t, err)
		reader := multipart.NewReader(bytes.NewReader(upstream.lastBody), params["boundary"])
		fields := map[string]string{}
		for {
			p, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			data, err := io.ReadAll(p)
			require.NoError(t, err)
			fields[p.FormName()] = string(data)
		}
		require.Equal(t, "markup-bytes", fields["markup"])
		require.Equal(t, "image-bytes", fields["image"])
		require.Equal(t, "gpt-image-2.6-flare", fields["model"])
		require.Equal(t, "/v1/images/edits", upstream.lastReq.URL.Path)
	})
}
