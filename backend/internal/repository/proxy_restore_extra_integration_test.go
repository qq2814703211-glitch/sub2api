//go:build integration

package repository

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (s *ProxyExpirySuite) TestRevertRestoresOriginProxyWithoutTouchingExtra() {
	original := s.mkProxy("original", service.FallbackModeDirect, nil, nil)
	backup := s.mkProxy("backup", service.FallbackModeNone, nil, nil)
	account := s.mkAccountWithProxy(backup)
	_, err := s.tx.ExecContext(s.ctx, `UPDATE accounts SET type='apikey',proxy_fallback_origin_id=$1,extra='{"keep_me":true,"custom_note":"untouched"}'::jsonb WHERE id=$2`, original, account)
	s.Require().NoError(err)
	repo := newAccountRepositoryWithSQL(s.tx.Client(), s.tx, nil)
	s.Require().NoError(repo.RevertProxyFallback(s.ctx, account))
	s.Require().Equal(&original, s.accountProxyID(account))
	var raw []byte
	s.Require().NoError(scanSingleRow(s.ctx, s.tx, `SELECT extra FROM accounts WHERE id=$1`, []any{account}, &raw))
	var extra map[string]any
	s.Require().NoError(json.Unmarshal(raw, &extra))
	s.Equal(true, extra["keep_me"])
	s.Equal("untouched", extra["custom_note"])
}

func (s *ProxyExpirySuite) TestRevertRepointsEveryAccountTypeToOriginProxy() {
	for _, tc := range []struct {
		name, kind string
		same       bool
		direct     bool
	}{
		{"changed API key", "apikey", false, false},
		{"direct API key", "apikey", false, true},
		{"same proxy", "apikey", true, false},
		{"oauth metadata", "oauth", false, false},
	} {
		s.Run(tc.name, func() {
			original := s.mkProxy("origin", service.FallbackModeNone, nil, nil)
			current := s.mkProxy("current", service.FallbackModeNone, nil, nil)
			if tc.same {
				current = original
			}
			account := s.mkAccountWithProxy(current)
			_, err := s.tx.ExecContext(s.ctx, `UPDATE accounts SET type=$1,proxy_fallback_origin_id=$2,extra='{"keep_me":true}'::jsonb WHERE id=$3`, tc.kind, original, account)
			s.Require().NoError(err)
			if tc.direct {
				_, err = s.tx.ExecContext(s.ctx, `UPDATE accounts SET proxy_id=NULL WHERE id=$1`, account)
				s.Require().NoError(err)
			}
			repo := newAccountRepositoryWithSQL(s.tx.Client(), s.tx, nil)
			s.Require().NoError(repo.RevertProxyFallback(s.ctx, account))
			var raw []byte
			s.Require().NoError(scanSingleRow(s.ctx, s.tx, `SELECT extra FROM accounts WHERE id=$1`, []any{account}, &raw))
			var extra map[string]any
			s.Require().NoError(json.Unmarshal(raw, &extra))
			s.Equal(true, extra["keep_me"])
			s.Equal(&original, s.accountProxyID(account))
		})
	}
}
