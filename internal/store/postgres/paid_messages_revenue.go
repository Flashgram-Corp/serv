package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
	"telesrv/internal/store/postgres/sqlcgen"
)

// PaidMessagesStore stores paid-message exceptions and revenue ledger state.
// Each row is an exception: who may skip Stars tolls for paid messages.
// scope_channel_id > 0 (granter_user_id = 0): channel staff-managed free passes
// for paid messages in that channel.
// scope_channel_id = 0 (granter_user_id > 0): caller-granted pass for the
// caller's own inbox (recipient grants sender free passage).
//
// Revenue accounting is delta-based: revenue and refunded_stars only ever grow
// monotonically, so a retried grant/revoke never double-counts. Net revenue for
// a scope/user is revenue - refunded_stars.
type PaidMessagesStore struct {
	db sqlcgen.DBTX
}

// NewPaidMessagesStore creates a new PaidMessagesStore.
func NewPaidMessagesStore(db sqlcgen.DBTX) *PaidMessagesStore {
	return &PaidMessagesStore{db: db}
}

// PaidMessagesRevenue reads committed paid and refunded Stars for one scope/user.
// Returns (paid, refunded, err).
func (s *PaidMessagesStore) PaidMessagesRevenue(ctx context.Context, userID int64, channelID, recipientID int64) (int64, int64, error) {
	if s == nil || s.db == nil || userID <= 0 {
		return 0, 0, domain.ErrStarsInvalidAmount
	}
	var paid, refunded int64
	err := s.db.QueryRow(ctx, `SELECT revenue, refunded_stars FROM paid_message_exceptions
WHERE scope_channel_id=$1 AND granter_user_id=$2 AND user_id=$3`,
		channelID, recipientID, userID).Scan(&paid, &refunded)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("read paid message revenue: %w", err)
	}
	return paid, refunded, nil
}

// HasPaidMessagesException returns true if userID has an active exception that
// exempts them from paying Stars for paid messages in the given scope.
// scope_channel_id > 0: channel staff-managed exception.
// scope_channel_id = 0: caller-granted private exception.
func (s *PaidMessagesStore) HasPaidMessagesException(ctx context.Context, channelID, granterUserID, userID int64) (bool, error) {
	if s == nil || s.db == nil || userID <= 0 {
		return false, nil
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM paid_message_exceptions
WHERE scope_channel_id=$1 AND granter_user_id=$2 AND user_id=$3)`,
		channelID, granterUserID, userID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check paid exception: %w", err)
	}
	return exists, nil
}

// SetPaidMessagesException grants or revokes a paid-message exception.
// requirePayment=false grants a free pass; requirePayment=true revokes it.
// grantedBy is the user who performed the grant/revoke.
// date is the Unix timestamp of the operation.
func (s *PaidMessagesStore) SetPaidMessagesException(ctx context.Context, channelID, granterUserID, userID, grantedBy int64, requirePayment bool, date int) error {
	if s == nil || s.db == nil || userID <= 0 || date <= 0 {
		return domain.ErrStarsInvalidAmount
	}
	if requirePayment {
		_, err := s.db.Exec(ctx, `DELETE FROM paid_message_exceptions
WHERE scope_channel_id=$1 AND granter_user_id=$2 AND user_id=$3`,
			channelID, granterUserID, userID)
		if err != nil {
			return fmt.Errorf("revoke paid exception: %w", err)
		}
		return nil
	}
	_, err := s.db.Exec(ctx, `INSERT INTO paid_message_exceptions(scope_channel_id,granter_user_id,user_id,granted_by,created_at,updated_at,revenue,refunded_stars)
VALUES($1,$2,$3,$4,$5,$5,0,0)
ON CONFLICT(scope_channel_id,granter_user_id,user_id)
DO UPDATE SET granted_by=EXCLUDED.granter_user_id, updated_at=EXCLUDED.updated_at`,
		channelID, granterUserID, userID, grantedBy, date)
	if err != nil {
		return fmt.Errorf("grant paid exception: %w", err)
	}
	return nil
}

// RefundPaidMessagesRevenue credits refunded_stars by delta for the given scope/user.
// Only the not-yet-refunded portion can be refunded again, so retried refunds
// never double-credit. Returns the actual delta applied.
func (s *PaidMessagesStore) RefundPaidMessagesRevenue(ctx context.Context, channelID, granterUserID, userID int64, date int) (int64, error) {
	if s == nil || s.db == nil || userID <= 0 || date <= 0 {
		return 0, domain.ErrStarsInvalidAmount
	}
	// Read current state under lock to compute the refundable delta.
	var paid, refunded int64
	err := s.db.QueryRow(ctx, `SELECT revenue, refunded_stars FROM paid_message_exceptions
WHERE scope_channel_id=$1 AND granter_user_id=$2 AND user_id=$3 FOR UPDATE`,
		channelID, granterUserID, userID).Scan(&paid, &refunded)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil // nothing to refund
	}
	if err != nil {
		return 0, fmt.Errorf("read paid exception for refund: %w", err)
	}
	refundable := paid - refunded
	if refundable <= 0 {
		return 0, nil
	}
	_, err = s.db.Exec(ctx, `UPDATE paid_message_exceptions
SET refunded_stars = refunded_stars + $1
WHERE scope_channel_id=$2 AND granter_user_id=$3 AND user_id=$4`,
		refundable, channelID, granterUserID, userID)
	if err != nil {
		return 0, fmt.Errorf("refund paid exception revenue: %w", err)
	}
	return refundable, nil
}


