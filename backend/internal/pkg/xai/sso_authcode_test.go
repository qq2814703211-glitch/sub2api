//go:build unit

package xai

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

type ssoAuthCodeFakeClient struct {
	t     *testing.T
	query url.Values
	calls []string
}

func (c *ssoAuthCodeFakeClient) Do(req *http.Request) (*http.Response, error) {
	c.calls = append(c.calls, req.Method+" "+req.URL.Path)
	switch {
	case req.URL.String() == SSOAccountsURL:
		require.Contains(c.t, req.Header.Get("Cookie"), "sso=sso-token")
		return ssoDeviceResponse(200, http.Header{"Set-Cookie": {"session=synthetic; Domain=x.ai; Path=/"}}, `{}`), nil
	case req.URL.Path == "/oauth2/authorize" && req.Method == http.MethodGet:
		c.query = req.URL.Query()
		require.Equal(c.t, "code", c.query.Get("response_type"))
		require.Equal(c.t, "S256", c.query.Get("code_challenge_method"))
		require.Equal(c.t, SSOBuildScope, c.query.Get("scope"))
		require.NotEmpty(c.t, c.query.Get("state"))
		return ssoDeviceResponse(302, http.Header{"Location": {"https://auth.x.ai/oauth2/consent"}}, ""), nil
	case req.URL.Path == "/oauth2/consent":
		require.Contains(c.t, req.Header.Get("Cookie"), "session=synthetic")
		return ssoDeviceResponse(200, nil, `<form></form>`), nil
	case req.URL.Path == "/oauth2/authorize" && req.Method == http.MethodPost:
		form := readSSODeviceForm(c.t, req)
		require.Equal(c.t, c.query.Get("state"), form.Get("state"))
		require.Equal(c.t, c.query.Get("code_challenge"), form.Get("code_challenge"))
		require.Equal(c.t, "https://auth.x.ai", req.Header.Get("Origin"))
		require.Equal(c.t, "https://auth.x.ai/oauth2/consent", req.Header.Get("Referer"))
		return ssoDeviceResponse(302, http.Header{"Location": {DefaultRedirectURI + "?code=synthetic-code&state=" + url.QueryEscape(c.query.Get("state"))}}, ""), nil
	case req.URL.String() == SSOTokenURL:
		form := readSSODeviceForm(c.t, req)
		require.Equal(c.t, "authorization_code", form.Get("grant_type"))
		require.Equal(c.t, "synthetic-code", form.Get("code"))
		sum := sha256.Sum256([]byte(form.Get("code_verifier")))
		require.Equal(c.t, c.query.Get("code_challenge"), base64.RawURLEncoding.EncodeToString(sum[:]))
		return ssoDeviceResponse(200, nil, `{"access_token":"access-token","refresh_token":"refresh-token","id_token":"id-token","expires_in":3600}`), nil
	default:
		c.t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
		return nil, nil
	}
}
func TestConvertSSOToBuildUsesAuthorizationCodePKCE(t *testing.T) {
	client := &ssoAuthCodeFakeClient{t: t}
	token, err := ConvertSSOToBuild(context.Background(), "sso=sso-token; ignored=1", &SSODeviceOptions{HTTPClient: client})
	require.NoError(t, err)
	require.Equal(t, "access-token", token.AccessToken)
	require.Equal(t, "refresh-token", token.RefreshToken)
	require.Equal(t, SSOBuildScope, token.Scope)
	require.Len(t, client.calls, 5)
	for _, call := range client.calls {
		require.NotContains(t, call, "/device/")
	}
}
