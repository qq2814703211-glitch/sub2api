package service

import "context"

// LiveLeaseTransferRequest carries one ordinary account/user/key reservation
// each. RequestID fields are the exact Redis members owned by the caller; an
// empty ID is only valid when the matching Max is 0 (the dimension is
// unlimited and has no ordinary member to move).
type LiveLeaseTransferRequest struct {
	AccountID        int64
	AccountMax       int
	AccountRequestID string
	UserID           int64
	UserMax          int
	UserRequestID    string
	APIKeyID         int64
	APIKeyMax        int
	KeyRequestID     string
	LeaseID          string
	// ReplacedAccountID is the account whose Live member is replaced when an
	// already-held joint Key/user lease migrates to a new account after a
	// retryable SDP failure. Zero means no previous account member is held.
	ReplacedAccountID int64
}

// LiveLeaseTransferCache is optionally implemented by the Live lease cache. It
// moves the exact ordinary members into one Live lease in a single atomic
// operation, so no user/account/key dimension is counted twice and a missing
// source fails closed.
type LiveLeaseTransferCache interface {
	AcquireLiveLeaseTransferring(ctx context.Context, request LiveLeaseTransferRequest) (bool, error)
	// MigrateLiveLeaseAccount keeps an existing joint Key/user Live lease and
	// atomically replaces its account member with the new ordinary account
	// reservation. The Key and user members are never released or re-queued.
	MigrateLiveLeaseAccount(ctx context.Context, request LiveLeaseTransferRequest) (bool, error)
	// ReleaseLiveLeaseAccount removes only the current account's Live member,
	// keeping the joint Key/user members for the next retry attempt.
	ReleaseLiveLeaseAccount(ctx context.Context, accountID int64, leaseID string) error
	// AbortLiveLeaseTransfer fences this exact transfer and removes its members
	// after an unknown acknowledgement. It runs caller-supplied bounded cleanup
	// and never touches another lease.
	AbortLiveLeaseTransfer(ctx context.Context, request LiveLeaseTransferRequest) error
}
