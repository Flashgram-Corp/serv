// Package grams 实现 Grams 本地账本应用服务：余额查询、贷记/借记、流水分页，
// 以及「惰性首读授予」起始余额（靠 gram_balances.granted 布尔幂等）。
package grams

import (
	"context"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// Service 是 Grams 账本应用服务。
type Service struct {
	store       store.GramsStore
	grantAmount int64
	now         func() time.Time
}

// Option 配置 Service。
type Option func(*Service)

// WithStartingGrant 设置惰性首读授予的起始余额；amount<=0 关闭自动授予。
func WithStartingGrant(amount int64) Option {
	return func(s *Service) { s.grantAmount = amount }
}

// WithClock 注入时钟（测试用）。
func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// NewService 创建 Grams 账本服务。
func NewService(st store.GramsStore, opts ...Option) *Service {
	s := &Service{store: st, grantAmount: domain.DefaultGramsStartingGrant, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) ensureGranted(ctx context.Context, userID int64) (domain.GramBalance, error) {
	if s.grantAmount > 0 {
		bal, _, err := s.store.EnsureGrant(ctx, userID, s.grantAmount, int(s.now().Unix()))
		return bal, err
	}
	return s.store.GetBalance(ctx, userID)
}

// GetBalance 返回账号余额，首读时惰性授予起始余额。
func (s *Service) GetBalance(ctx context.Context, userID int64) (domain.GramBalance, error) {
	return s.ensureGranted(ctx, userID)
}

// Credit 为账号入账（amount>0）。
func (s *Service) Credit(ctx context.Context, userID, amount int64, reason string, peer domain.Peer, title, desc string) (domain.GramBalance, error) {
	if amount <= 0 {
		return domain.GramBalance{}, domain.ErrGramsInvalidAmount
	}
	return s.store.Credit(ctx, userID, amount, reason, peer, int(s.now().Unix()), title, desc)
}

// Debit 从账号扣款（amount>0），余额不足返回 domain.ErrGramsInsufficient。
func (s *Service) Debit(ctx context.Context, userID, amount int64, reason string, peer domain.Peer, title, desc string) (domain.GramBalance, error) {
	if amount <= 0 {
		return domain.GramBalance{}, domain.ErrGramsInvalidAmount
	}
	if _, err := s.ensureGranted(ctx, userID); err != nil {
		return domain.GramBalance{}, err
	}
	return s.store.Debit(ctx, userID, amount, reason, peer, int(s.now().Unix()), title, desc)
}

// ListTransactions 按 keyset 分页返回流水。
func (s *Service) ListTransactions(ctx context.Context, userID int64, offset int64, limit int) (domain.GramTransactionPage, error) {
	if _, err := s.ensureGranted(ctx, userID); err != nil {
		return domain.GramTransactionPage{}, err
	}
	return s.store.ListTransactions(ctx, userID, offset, limit)
}
