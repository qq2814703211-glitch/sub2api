package xai

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// SSO to token conversion over the authorization_code + PKCE flow.
//
// The device flow's final approve is gated by a Cloudflare castle token that
// only a real browser can mint, so it can never complete server-side. The
// authorization_code consent page carries no castle field, which makes this the
// only flow that can run without a browser.

const ssoAuthorizeURL = OAuthIssuer + "/oauth2/authorize"

func (f *ssoDeviceFlow) convertViaAuthCode(ctx context.Context) (*TokenResponse, error) {
	if err := f.validateSSO(ctx); err != nil {
		return nil, err
	}

	verifier, challenge, state, nonce, err := pkcePair()
	if err != nil {
		return nil, err
	}

	query := url.Values{
		"client_id":             {DefaultClientID},
		"response_type":         {"code"},
		"redirect_uri":          {DefaultRedirectURI},
		"scope":                 {SSOBuildScope},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"nonce":                 {nonce},
	}

	status, location, body, err := f.raw(ctx, http.MethodGet, ssoAuthorizeURL+"?"+query.Encode(), nil, nil)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("start xAI authorization: %w", SSOHTTPError{Status: status})
	}

	code := codeFromLocation(location)
	consentURL := ""
	if code == "" {
		switch {
		case strings.Contains(location, "sign-in") || strings.Contains(location, "sign-up"):
			return nil, ErrSSOUnauthorized
		case strings.Contains(location, "/consent"):
			consentURL = location
		case status == http.StatusOK && strings.Contains(string(body), `<form`):
			// Consent rendered in place; treat the requested URL as consent.
			consentURL = ssoAuthorizeURL + "?" + query.Encode()
		default:
			return nil, fmt.Errorf("xAI authorization did not reach consent (status %d)", status)
		}
	}

	if code == "" {
		status, _, _, err = f.raw(ctx, http.MethodGet, consentURL, nil, nil)
		if err != nil {
			return nil, err
		}
		if status >= 400 {
			return nil, fmt.Errorf("open xAI consent page: %w", SSOHTTPError{Status: status})
		}

		approval := url.Values{
			"client_id":             {DefaultClientID},
			"redirect_uri":          {DefaultRedirectURI},
			"response_type":         {"code"},
			"scope":                 {SSOBuildScope},
			"state":                 {state},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
			"nonce":                 {nonce},
			"principal_type":        {"User"},
			"principal_id":          {""},
			"referrer":              {""},
		}
		consentParsed, _ := url.Parse(consentURL)
		headers := http.Header{}
		if consentParsed != nil {
			headers.Set("Origin", consentParsed.Scheme+"://"+consentParsed.Host)
		}
		headers.Set("Referer", consentURL)

		status, location, body, err = f.raw(ctx, http.MethodPost, ssoAuthorizeURL, approval, headers)
		if err != nil {
			return nil, err
		}
		if status >= 400 {
			return nil, fmt.Errorf("approve xAI consent: %w", SSOHTTPError{Status: status})
		}
		code = codeFromLocation(location)
		if code == "" {
			return nil, fmt.Errorf("xAI consent did not return a code (status %d): %s", status, firstNonEmpty(strings.TrimSpace(string(body)), "empty body"))
		}
	}

	return f.exchangeCode(ctx, code, verifier)
}

func (f *ssoDeviceFlow) validateSSO(ctx context.Context) error {
	status, finalURL, _, err := f.do(ctx, http.MethodGet, SSOAccountsURL, nil)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized || strings.Contains(finalURL, "sign-in") || strings.Contains(finalURL, "sign-up") {
		return ErrSSOUnauthorized
	}
	if status < 200 || status >= 400 {
		return fmt.Errorf("validate Grok Web SSO: %w", SSOHTTPError{Status: status})
	}
	return nil
}

func (f *ssoDeviceFlow) exchangeCode(ctx context.Context, code, verifier string) (*TokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {DefaultClientID},
		"redirect_uri":  {DefaultRedirectURI},
		"code_verifier": {verifier},
	}
	status, _, body, err := f.raw(ctx, http.MethodPost, SSOTokenURL, form, nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("exchange xAI authorization code: %w", SSOHTTPError{Status: status})
	}
	var payload struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		IDToken          string `json:"id_token"`
		TokenType        string `json:"token_type"`
		ExpiresIn        int64  `json:"expires_in"`
		Scope            string `json:"scope"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse xAI token response: %w", err)
	}
	if payload.AccessToken == "" {
		return nil, fmt.Errorf("xAI token exchange failed: %s", firstNonEmpty(payload.ErrorDescription, payload.Error, "empty access token"))
	}
	expiresIn := payload.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = int64(ssoDefaultTokenTTL.Seconds())
	}
	return &TokenResponse{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		IDToken:      payload.IDToken,
		TokenType:    firstNonEmpty(payload.TokenType, "Bearer"),
		ExpiresIn:    expiresIn,
		Scope:        firstNonEmpty(payload.Scope, SSOBuildScope),
	}, nil
}

// raw performs one request without following redirects, so the Location of a
// code redirect can be inspected (the callback host is not an xAI host).
func (f *ssoDeviceFlow) raw(ctx context.Context, method, endpoint string, form url.Values, headers http.Header) (int, string, []byte, error) {
	if !safeXAIAuthURL(endpoint) {
		return 0, "", nil, fmt.Errorf("xAI OAuth URL is not trusted")
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return 0, "", nil, err
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")
	request.Header.Set("User-Agent", f.userAgent)
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	if cookie := f.cookieHeader(request.URL); cookie != "" {
		request.Header.Set("Cookie", cookie)
	}

	response, err := f.client.Do(request)
	if err != nil {
		return 0, "", nil, err
	}
	f.captureCookies(request.URL, response)
	data, readErr := io.ReadAll(io.LimitReader(response.Body, ssoMaxAuthBody+1))
	_ = response.Body.Close()
	if readErr != nil {
		return response.StatusCode, "", nil, readErr
	}
	if len(data) > ssoMaxAuthBody {
		return response.StatusCode, "", nil, fmt.Errorf("xAI OAuth response exceeds 2 MiB")
	}
	return response.StatusCode, strings.TrimSpace(response.Header.Get("Location")), data, nil
}

func codeFromLocation(location string) string {
	if location == "" {
		return ""
	}
	parsed, err := url.Parse(location)
	if err != nil {
		return ""
	}
	return parsed.Query().Get("code")
}

func pkcePair() (verifier, challenge, state, nonce string, err error) {
	verifier, err = randomB64(48)
	if err != nil {
		return "", "", "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	if state, err = randomB64(16); err != nil {
		return "", "", "", "", err
	}
	if nonce, err = randomB64(16); err != nil {
		return "", "", "", "", err
	}
	return verifier, challenge, state, nonce, nil
}

func randomB64(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
