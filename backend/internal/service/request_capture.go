package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
)

const SettingKeyRequestCapture = "request_capture_config"

// CaptureSettings is independent of gateway and billing configuration. An
// omitted setting never enables body collection on an existing installation.
func captureSettingsKey() string {
	return SettingKeyRequestCapture + ":" + strings.TrimSpace(os.Getenv("SUB2API_CAPTURE_INSTANCE"))
}

type CaptureSettings struct {
	Enabled       bool  `json:"enabled"`
	QuotaMiB      int64 `json:"quota_mib"`
	RetentionDays int   `json:"retention_days"`
}

func (c CaptureSettings) captureConfig() requestcapture.Config {
	return requestcapture.Config{Enabled: c.Enabled, QuotaMiB: c.QuotaMiB, RetentionDays: c.RetentionDays}
}

func (s *SettingService) GetCaptureSettings(ctx context.Context) (CaptureSettings, error) {
	c := CaptureSettings{QuotaMiB: 1024, RetentionDays: 7}
	if s == nil || s.settingRepo == nil {
		return c, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, captureSettingsKey())
	if errors.Is(err, ErrSettingNotFound) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if raw != "" {
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			return c, err
		}
	}
	return c, c.captureConfig().Validate()
}

func (s *SettingService) UpdateCaptureSettings(ctx context.Context, c CaptureSettings) error {
	if err := c.captureConfig().Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err = s.settingRepo.SetMultiple(ctx, map[string]string{captureSettingsKey(): string(raw)}); err != nil {
		return err
	}
	if s.requestCaptureRuntime != nil {
		s.requestCaptureRuntime.manager.ApplyConfig(c.captureConfig())
	}
	return nil
}

type captureRuntime struct{ manager *requestcapture.Manager }

// InitializeRequestCapture is called once during router composition, after
// migration validation. Optional diagnostic storage must not stop the gateway.
func (s *SettingService) InitializeRequestCapture(db *sql.DB) (*requestcapture.Manager, error) {
	if db == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	c, err := s.GetCaptureSettings(ctx)
	cancel()
	if err != nil {
		return nil, err
	}
	dir := os.Getenv("DATA_DIR")
	if dir == "" {
		dir = "./data"
	}
	// Each blue/green slot must explicitly own a stable local storage identity.
	// PID directories lose retention ownership after restart; no implicit slot is
	// safe across concurrent rolling versions, so unconfigured storage stays off.
	instance := strings.TrimSpace(os.Getenv("SUB2API_CAPTURE_INSTANCE"))
	if instance == "" {
		return nil, nil
	}
	if filepath.Base(instance) != instance || instance == "." || instance == ".." || len(instance) > 96 {
		return nil, errors.New("invalid capture instance identifier")
	}
	m, err := requestcapture.New(&requestcapture.SQLStore{DB: db}, filepath.Join(dir, "request-captures", instance), c.captureConfig())
	if err != nil {
		return nil, err
	}
	s.requestCaptureRuntime = &captureRuntime{manager: m}
	return m, nil
}

func (s *SettingService) StopRequestCapture() {
	if s == nil || s.requestCaptureRuntime == nil {
		return
	}
	s.requestCaptureRuntime.manager.Close()
}
