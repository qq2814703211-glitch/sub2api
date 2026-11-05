//go:build unit

package xai

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Fixtures exercise the active public entrypoint without making OAuth requests.
type ssoAuthCodeFakeClient struct {
	t             *testing.T
	challenge     string
	state         string
	nonce         string
	authorized    bool
	tokenCalls    int
	cookieHeaders []string
}

func (c *ssoAuthCodeFakeClient) Do(req *http.Request) (*http.Response, error) {
	c.t.Helper()
	c.cookieHeaders = append(c.cookieHeaders, req.Header.Get("Cookie"))
	if req.URL.String() == SSOAccountsURL {
		require.Equal(c.t, http.MethodGet, req.Method)
		return ssoDeviceResponse(http.StatusOK, http.Header{"Set-Cookie": {"session=web-session; Domain=x.ai; Path=/"}}, `{}`), nil
	}
	if req.URL.String() == SSOTokenURL {
		require.Equal(c.t, http.MethodPost, req.Method)
		c.tokenCalls++
		values := readSSODeviceForm(c.t, req)
		require.Equal(c.t, "authorization_code", values.Get("grant_type"))
		require.Equal(c.t, "test-code", values.Get("code"))
		require.Equal(c.t, DefaultClientID, values.Get("client_id"))
		require.Equal(c.t, DefaultRedirectURI, values.Get("redirect_uri"))
		verifier := values.Get("code_verifier")
		require.NotEmpty(c.t, verifier)
		sum := sha256.Sum256([]byte(verifier))
		require.Equal(c.t, c.challenge, base64.RawURLEncoding.EncodeToString(sum[:]))
		require.True(c.t, c.authorized)
		require.Empty(c.t, req.Header.Get("Origin"))
		require.Empty(c.t, req.Header.Get("Referer"))
		return ssoDeviceResponse(http.StatusOK, nil, `{"access_token":"access-token","refresh_token":"refresh-token","id_token":"id-token","token_type":"Bearer","expires_in":3600,"scope":"`+SSOBuildScope+`"}`), nil
	}
	if req.URL.Scheme+"://"+req.URL.Host+req.URL.Path == ssoAuthorizeURL {
		if req.Method == http.MethodGet {
			values := req.URL.Query()
			require.Equal(c.t, DefaultClientID, values.Get("client_id"))
			require.Equal(c.t, DefaultRedirectURI, values.Get("redirect_uri"))
			require.Equal(c.t, SSOBuildScope, values.Get("scope"))
			require.Equal(c.t, "code", values.Get("response_type"))
			require.Equal(c.t, "S256", values.Get("code_challenge_method"))
			c.challenge, c.state, c.nonce = values.Get("code_challenge"), values.Get("state"), values.Get("nonce")
			require.NotEmpty(c.t, c.challenge)
			require.NotEmpty(c.t, c.state)
			require.NotEmpty(c.t, c.nonce)
			return ssoDeviceResponse(http.StatusFound, http.Header{"Location": {OAuthIssuer + "/oauth2/consent?flow=test"}}, ""), nil
		}
		require.Equal(c.t, http.MethodPost, req.Method)
		values := readSSODeviceForm(c.t, req)
		require.Equal(c.t, c.state, values.Get("state"))
		require.Equal(c.t, c.challenge, values.Get("code_challenge"))
		require.Equal(c.t, c.nonce, values.Get("nonce"))
		require.Equal(c.t, "User", values.Get("principal_type"))
		require.Equal(c.t, OAuthIssuer, req.Header.Get("Origin"))
		require.Equal(c.t, OAuthIssuer+"/oauth2/consent?flow=test", req.Header.Get("Referer"))
		c.authorized = true
		return ssoDeviceResponse(http.StatusSeeOther, http.Header{"Location": {DefaultRedirectURI + "?code=test-code&state=" + url.QueryEscape(c.state)}}, ""), nil
	}
	if req.URL.String() == OAuthIssuer+"/oauth2/consent?flow=test" {
		require.Equal(c.t, http.MethodGet, req.Method)
		return ssoDeviceResponse(http.StatusOK, http.Header{"Set-Cookie": {"csrf=auth-csrf; Path=/"}}, `<html><form>consent</form></html>`), nil
	}
	c.t.Fatalf("unexpected OAuth fixture request: %s %s", req.Method, req.URL.String())
	return nil, nil
}

func TestConvertSSOToBuildCompletesAuthorizationCodePKCE(t *testing.T) {
	t.Setenv(EnvClientID, "")
	client := &ssoAuthCodeFakeClient{t: t}
	token, err := ConvertSSOToBuild(context.Background(), "sso=sso-token; ignored=1", &SSODeviceOptions{HTTPClient: client})
	require.NoError(t, err)
	require.Equal(t, "access-token", token.AccessToken)
	require.Equal(t, "refresh-token", token.RefreshToken)
	require.Equal(t, "id-token", token.IDToken)
	require.Equal(t, SSOBuildScope, token.Scope)
	require.Equal(t, int64(3600), token.ExpiresIn)
	require.Equal(t, 1, client.tokenCalls)
	require.Contains(t, client.cookieHeaders[0], "sso=sso-token")
	require.Contains(t, client.cookieHeaders[0], "sso-rw=sso-token")
	last := client.cookieHeaders[len(client.cookieHeaders)-1]
	require.Contains(t, last, "session=web-session")
	require.Contains(t, last, "csrf=auth-csrf")
	for _, cookie := range client.cookieHeaders {
		require.False(t, strings.Contains(cookie, "ignored="))
	}
}
