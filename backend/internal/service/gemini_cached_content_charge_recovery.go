package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GeminiCachedContentChargeRepository isolates durable billing from metadata fixtures.
type GeminiCachedContentChargeRepository interface {
	CommitChargedMutation(context.Context, int64, string, time.Time, *GeminiCachedContentChargeIntent) error
	ClaimCharge(context.Context, int64, string, string, time.Duration) (*GeminiCachedContent, error)
	CompleteCharge(context.Context, int64, string, string) error
	FailCharge(context.Context, int64, string, string) error
	ListPendingCharges(context.Context, int) ([]*GeminiCachedContent, error)
}

func (s *GeminiCachedContentService) SetBillingGateway(gateway *GatewayService) {
	s.billingGateway = gateway
}

func validateCachedContentCharge(record *GeminiCachedContent, intent *GeminiCachedContentChargeIntent) error {
	if record == nil || intent == nil || intent.Usage.GroupID == nil ||
		intent.Command.UserID != record.UserID || intent.Command.APIKeyID != record.APIKeyID || intent.Command.AccountID != record.AccountID ||
		intent.Usage.UserID != record.UserID || intent.Usage.APIKeyID != record.APIKeyID || intent.Usage.AccountID != record.AccountID || *intent.Usage.GroupID != record.GroupID ||
		intent.Command.RequestID != intent.Usage.RequestID || !strings.HasPrefix(intent.Command.RequestID, GeminiCachedContentUsageRequestIDPrefix) {
		return errors.New("cached content charge owner conflict")
	}
	return nil
}

func (s *GeminiCachedContentService) SaveCharged(ctx context.Context, record *GeminiCachedContent, intent *GeminiCachedContentChargeIntent) error {
	if _, ok := s.repo.(GeminiCachedContentChargeRepository); !ok {
		return errors.New("durable cached content charges are unsupported")
	}
	if err := validateCachedContentCharge(record, intent); err != nil {
		return err
	}
	record.BillingPending, record.ChargeIntent = true, intent
	target := record.ExpireTime
	record.ChargeTargetExpire = &target
	return s.repo.Create(ctx, record)
}

func (s *GeminiCachedContentService) CommitChargedMutation(ctx context.Context, m *GeminiCachedContentMutation, target time.Time, intent *GeminiCachedContentChargeIntent) error {
	repo, ok := s.repo.(GeminiCachedContentChargeRepository)
	if !ok {
		return errors.New("durable cached content charges are unsupported")
	}
	if m == nil {
		return ErrGeminiCachedContentMutationConflict
	}
	if err := validateCachedContentCharge(m.record, intent); err != nil {
		return err
	}
	if err := repo.CommitChargedMutation(ctx, m.record.ID, m.token, target, intent); err != nil {
		return err
	}
	m.record.BillingPending, m.record.ChargeIntent, m.record.ChargeTargetExpire = true, intent, &target
	return nil
}

func (s *GeminiCachedContentService) SettleCharge(ctx context.Context, record *GeminiCachedContent) error {
	if record == nil || record.ChargeIntent == nil {
		return errors.New("cached content charge is missing")
	}
	if s.billingGateway == nil {
		return errors.New("cached content billing service is unavailable")
	}
	repo, ok := s.repo.(GeminiCachedContentChargeRepository)
	if !ok {
		return errors.New("durable cached content charges are unsupported")
	}
	token, err := NewGeminiCachedContentPublicID()
	if err != nil {
		return err
	}
	request := record.ChargeIntent.Command.RequestID
	claimed, err := repo.ClaimCharge(ctx, record.ID, request, token, geminiCachedContentMutationLease)
	if err != nil {
		return err
	}
	if claimed == nil {
		return ErrGeminiCachedContentMutationConflict
	}
	err = validateCachedContentCharge(claimed, claimed.ChargeIntent)
	if err == nil {
		err = s.billingGateway.ApplyCachedContentCharge(ctx, claimed.ChargeIntent)
	}
	if err == nil {
		err = repo.CompleteCharge(ctx, record.ID, request, token)
	}
	if err != nil {
		failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return errors.Join(err, repo.FailCharge(failCtx, record.ID, token, err.Error()))
	}
	if claimed.ChargeTargetExpire != nil {
		record.ExpireTime = *claimed.ChargeTargetExpire
	}
	record.BillingPending, record.ChargeIntent, record.ChargeTargetExpire = false, nil, nil
	return nil
}

func (s *GeminiCachedContentService) RecoverPendingCharges(ctx context.Context) (int, error) {
	select {
	case <-s.stopCh:
		return 0, nil
	default:
	}
	repo, ok := s.repo.(GeminiCachedContentChargeRepository)
	if !ok {
		return 0, nil
	}
	listCtx, cancel := context.WithTimeout(ctx, geminiCachedContentMutationRecoveryTimeout)
	records, err := repo.ListPendingCharges(listCtx, geminiCachedContentMutationRecoveryBatch)
	cancel()
	if err != nil {
		return 0, err
	}
	count := 0
	var failures []error
	for _, record := range records {
		select {
		case <-s.stopCh:
			return count, errors.Join(failures...)
		default:
		}
		workCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := s.SettleCharge(workCtx, record)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("cache charge %d: %w", record.ID, err))
		} else {
			count++
		}
	}
	return count, errors.Join(failures...)
}
