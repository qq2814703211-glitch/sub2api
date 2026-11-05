package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
)

func manualAccountOpsGroupID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "manual-" + hex.EncodeToString(sum[:])[:32]
}

func legacyManualAccountOpsAccountGroupID(accountID int64) string {
	return manualAccountOpsGroupID("account:" + strconv.FormatInt(accountID, 10))
}

func legacyAccountOpsGroupID(id string) bool {
	return strings.HasPrefix(id, "auto:") || strings.HasPrefix(id, "account:")
}

func cloneAccountOpsGroups(groups []AccountOpsGroup) []AccountOpsGroup {
	cloned := make([]AccountOpsGroup, len(groups))
	for i, group := range groups {
		cloned[i] = group
		cloned[i].AccountIDs = append([]int64(nil), group.AccountIDs...)
	}
	return cloned
}

func hasAccountOpsGroupMember(groups []AccountOpsGroup, accountID int64) bool {
	for _, group := range groups {
		if slices.Contains(group.AccountIDs, accountID) {
			return true
		}
	}
	return false
}

func accountOpsGroupMemberIndex(groups []AccountOpsGroup, id string) int {
	for i := range groups {
		if groups[i].ID == id {
			return i
		}
	}
	return -1
}

func accountOpsGroupSite(group AccountOpsGroup, accounts map[int64]*Account) (provider, site string) {
	provider = strings.TrimSpace(group.Provider)
	site = canonicalAccountOpsSite(group.Site)
	for _, id := range group.AccountIDs {
		if account := accounts[id]; account != nil {
			accountProvider, accountSite, _ := accountOpsUpstreamIdentity(account)
			if provider == "" {
				provider = accountProvider
			}
			if site == "" {
				site = accountSite
			}
			break
		}
	}
	if provider == "" {
		provider = "sub2api"
	}
	return provider, site
}

func appendUniqueAccountOpsGroupMember(group *AccountOpsGroup, accountID int64) bool {
	if slices.Contains(group.AccountIDs, accountID) {
		return false
	}
	group.AccountIDs = append(group.AccountIDs, accountID)
	return true
}

func copyBalanceRuleForAccount(rule AccountOpsBalanceRule, accountID int64) AccountOpsBalanceRule {
	rule.AccountID = accountID
	if rule.NotifyAlert != nil {
		rule.NotifyAlert = opsBool(*rule.NotifyAlert)
	}
	if rule.NotifyRecovery != nil {
		rule.NotifyRecovery = opsBool(*rule.NotifyRecovery)
	}
	return rule
}

func copyQuotaRuleForAccount(rule AccountOpsQuotaRule, accountID int64) AccountOpsQuotaRule {
	rule.AccountID = accountID
	if rule.NotifyAlert != nil {
		rule.NotifyAlert = opsBool(*rule.NotifyAlert)
	}
	if rule.NotifyRecovery != nil {
		rule.NotifyRecovery = opsBool(*rule.NotifyRecovery)
	}
	return rule
}

func (s *AccountOpsService) ensureManualAccountOpsGroups(ctx context.Context) (AccountOpsConfig, error) {
	if s.accounts == nil {
		return s.currentConfig(), nil
	}
	accounts, err := s.listAccountOpsAccounts(ctx)
	if err != nil {
		return AccountOpsConfig{}, err
	}
	return s.ensureManualAccountOpsGroupsFromAccounts(ctx, accounts)
}

func (s *AccountOpsService) ensureManualAccountOpsGroupsFromAccounts(ctx context.Context, accounts []Account) (AccountOpsConfig, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	c, err := s.loadConfig(ctx)
	if err == nil {
		s.config.Store(c)
	}
	return c, err
}
