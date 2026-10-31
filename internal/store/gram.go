package store

import (
	"context"

	"telesrv/internal/domain"
)

// GramsStore 持久化 Grams 本地账本：per-user 余额 + 交易流水。
// 借记/贷记/授予必须各自在单个事务内原子完成（余额与流水不漂移）。
type GramsStore interface {
	// GetBalance 返回账号当前余额；无行时返回零值（Balance 0, Granted false）。
	GetBalance(ctx context.Context, userID int64) (domain.GramBalance, error)
	// EnsureGrant 幂等地应用一次起始授予：仅当从未授予时贷记 amount 并置 granted=true、
	// 写一条 grant 流水，全在单事务内完成。返回最新余额 + 本次是否实际授予。
	EnsureGrant(ctx context.Context, userID, amount int64, date int) (domain.GramBalance, bool, error)
	// Credit 在单事务内为账号入账（amount>0）并写流水（amount=+x）；余额行不存在则创建。
	Credit(ctx context.Context, userID, amount int64, reason string, peer domain.Peer, date int, title, desc string) (domain.GramBalance, error)
	// Debit 在单事务内做 SELECT ... FOR UPDATE 充足性检查后扣款（amount>0），写流水（amount=-x）。
	// 余额不足返回 domain.ErrGramsInsufficient。
	Debit(ctx context.Context, userID, amount int64, reason string, peer domain.Peer, date int, title, desc string) (domain.GramBalance, error)
	// ListTransactions 按 keyset 分页，返回一页流水 + 当前余额。
	ListTransactions(ctx context.Context, userID int64, offset int64, limit int) (domain.GramTransactionPage, error)
}
