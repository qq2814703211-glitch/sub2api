//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLoginDoesNotArchivePassword(t *testing.T) {
	t.Chdir(t.TempDir())
	directory := filepath.Join(t.TempDir(), "copies")
	legacyConfig := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(legacyConfig, []byte("{}\n"), 0o600))
	t.Setenv("CONFIG_FILE", legacyConfig)
	viper.Reset()
	t.Cleanup(viper.Reset)
	loaded, err := config.LoadForBootstrap()
	require.NoError(t, err)
	const (
		email    = "mfa-copy@example.com"
		password = "synthetic-mfa-password"
		secret   = "JBSWY3DPEHPK3PXP"
	)
	cache := &oauthPendingFlowTotpCacheStub{}
	encryptor, err := repository.NewAESEncryptor(&config.Config{
		Totp: config.TotpConfig{EncryptionKey: strings.Repeat("42", 32)},
	})
	require.NoError(t, err)

	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: map[string]string{service.SettingKeyTotpEnabled: "true"},
		totpCache:     cache,
		totpEncryptor: encryptor,
	})
	handler = NewAuthHandler(loaded, handler.authService, handler.userService, handler.settingSvc, handler.promoService, handler.redeemService, handler.totpService, handler.userAttributeService)
	ctx := context.Background()
	passwordHash, err := handler.authService.HashPassword(password)
	require.NoError(t, err)
	encryptedSecret, err := encryptor.Encrypt(secret)
	require.NoError(t, err)
	user, err := client.User.Create().
		SetEmail(email).
		SetPasswordHash(passwordHash).
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		SetBalance(1).
		SetConcurrency(1).
		SetTotpEnabled(false).
		SetTotpSecretEncrypted(encryptedSecret).
		SetTotpEnabledAt(time.Now().UTC()).
		Save(ctx)
	require.NoError(t, err)

	loginBody, err := json.Marshal(LoginRequest{Email: email, Password: password})
	require.NoError(t, err)
	login := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		loginContext, _ := gin.CreateTestContext(recorder)
		loginContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
		loginContext.Request.Header.Set("Content-Type", "application/json")
		handler.Login(loginContext)
		return recorder
	}
	recorder := login()
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotEmpty(t, decodeJSONResponseData(t, recorder)["access_token"])
	require.NoDirExists(t, "data/password-copies")
	require.NoDirExists(t, directory)
	err = client.User.UpdateOneID(user.ID).SetTotpEnabled(true).Exec(ctx)
	require.NoError(t, err)
	loginRecorder := login()

	require.Equal(t, http.StatusOK, loginRecorder.Code)
	loginData := decodeJSONResponseData(t, loginRecorder)
	require.Equal(t, true, loginData["requires_2fa"])
	tempToken, ok := loginData["temp_token"].(string)
	require.True(t, ok)
	require.NotEmpty(t, tempToken)
	session, err := cache.GetLoginSession(ctx, tempToken)
	require.NoError(t, err)
	require.NotNil(t, session)
	encoded, err := json.Marshal(session)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &fields))
	require.Len(t, fields, 3)
	for _, key := range []string{"UserID", "Email", "TokenExpiry"} {
		require.Contains(t, fields, key)
	}

	wrongBody := bytes.NewBufferString(`{"temp_token":"` + tempToken + `","totp_code":"abcdef"}`)
	wrongRecorder := httptest.NewRecorder()
	wrongContext, _ := gin.CreateTestContext(wrongRecorder)
	wrongContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa", wrongBody)
	wrongContext.Request.Header.Set("Content-Type", "application/json")
	handler.Login2FA(wrongContext)
	require.Equal(t, http.StatusBadRequest, wrongRecorder.Code)

	code, err := totp.GenerateCode(secret, time.Now().UTC())
	require.NoError(t, err)
	validBody := bytes.NewBufferString(`{"temp_token":"` + tempToken + `","totp_code":"` + code + `"}`)
	validRecorder := httptest.NewRecorder()
	validContext, _ := gin.CreateTestContext(validRecorder)
	validContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa", validBody)
	validContext.Request.Header.Set("Content-Type", "application/json")
	handler.Login2FA(validContext)

	require.Equal(t, http.StatusOK, validRecorder.Code)
	validData := decodeJSONResponseData(t, validRecorder)
	require.NotEmpty(t, validData["access_token"])
	require.NoDirExists(t, "data/password-copies")
	require.NoDirExists(t, directory)
	remaining, err := cache.GetLoginSession(ctx, tempToken)
	require.NoError(t, err)
	require.Nil(t, remaining)
}
