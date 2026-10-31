package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"telesrv/internal/domain"
)

// GramsStore 是 Grams 本地账本的 PostgreSQL 实现。
type GramsStore struct {
	pool *pgxpool.Pool
}

// NewGramsStore 创建 Grams 账本 store。
func NewGramsStore(pool *pgxpool.Pool) *GramsStore {
	return &GramsStore{pool: pool}
}

// GetBalance 返回账号当前余额；无行时返回零值。
func (s *GramsStore) GetBalance(ctx context.Context, userID int64) (domain.GramBalance, error) {
	if s == nil || s.pool == nil || userID <= 0 {
		return domain.GramBalance{}, nil
	}
	var balance int64
	var granted bool
	err := s.pool.QueryRow(ctx, `SELECT balance, granted FROM gram_balances WHERE user_id=$1`, userID).
		Scan(&balance, &granted)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.GramBalance{UserID: userID}, nil
	}
	if err != nil {
		return domain.GramBalance{}, fmt.Errorf("grams: get balance: %w", err)
	}
	return domain.GramBalance{UserID: userID, Balance: balance, Granted: granted}, nil
}

// EnsureGrant 幂等应用一次起始授予。
func (s *GramsStore) EnsureGrant(ctx context.Context, userID, amount int64, date int) (domain.GramBalance, bool, error) {
	if s == nil || s.pool == nil || userID <= 0 {
		return domain.GramBalance{}, false, nil
	}
	if amount <= 0 {
		bal, err := s.GetBalance(ctx, userID)
		return bal, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.GramBalance{}, false, fmt.Errorf("grams: begin grant: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var balance int64
	err = tx.QueryRow(ctx, `SELECT balance FROM gram_balances WHERE user_id=$1 FOR UPDATE`, userID).Scan(&balance)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := tx.Exec(ctx, `INSERT INTO gram_balances(user_id,balance,granted,updated_at) VALUES($1,$2,true,now())`, userID, amount); err != nil {
			return domain.GramBalance{}, false, fmt.Errorf("grams: grant insert: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO gram_transactions(user_id,amount,reason,title,description,date) VALUES($1,$2,'grant',$3,'',$4)`,
			userID, amount, "Starting balance", date); err != nil {
			return domain.GramBalance{}, false, fmt.Errorf("grams: grant tx: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.GramBalance{}, false, fmt.Errorf("grams: grant commit: %w", err)
		}
		return domain.GramBalance{UserID: userID, Balance: amount, Granted: true}, true, nil
	case err != nil:
		return domain.GramBalance{}, false, fmt.Errorf("grams: grant read: %w", err)
	}
	var granted bool
	if err := tx.QueryRow(ctx, `SELECT granted FROM gram_balances WHERE user_id=$1`, userID).Scan(&granted); err != nil {
		return domain.GramBalance{}, false, fmt.Errorf("grams: grant flag: %w", err)
	}
	if granted {
		return domain.GramBalance{UserID: userID, Balance: balance, Granted: true}, false, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE gram_balances SET balance=balance+$1, granted=true, updated_at=now() WHERE user_id=$2`, amount, userID); err != nil {
		return domain.GramBalance{}, false, fmt.Errorf("grams: grant update: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO gram_transactions(user_id,amount,reason,title,description,date) VALUES($1,$2,'grant',$3,'',$4)`,
		userID, amount, "Starting balance", date); err != nil {
		return domain.GramBalance{}, false, fmt.Errorf("grams: grant tx: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.GramBalance{}, false, fmt.Errorf("grams: grant commit: %w", err)
	}
	return domain.GramBalance{UserID: userID, Balance: balance + amount, Granted: true}, true, nil
}

// Credit 在单事务内入账并写流水。
func (s *GramsStore) Credit(ctx context.Context, userID, amount int64, reason string, peer domain.Peer, date int, title, desc string) (domain.GramBalance, error) {
	if s == nil || s.pool == nil || userID <= 0 || amount <= 0 {
		return domain.GramBalance{}, domain.ErrGramsInvalidAmount
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.GramBalance{}, fmt.Errorf("grams: begin credit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO gram_balances(user_id,balance,granted,updated_at) VALUES($1,$2,true,now())
ON CONFLICT(user_id) DO UPDATE SET balance=gram_balances.balance+EXCLUDED.balance, updated_at=now()`,
		userID, amount); err != nil {
		return domain.GramBalance{}, fmt.Errorf("grams: credit balance: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO gram_transactions(user_id,peer_type,peer_id,amount,reason,title,description,date)
VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		userID, peer.Type, peer.ID, amount, reason, title, desc, date); err != nil {
		return domain.GramBalance{}, fmt.Errorf("grams: credit tx: %w", err)
	}
	var balance int64
	if err := tx.QueryRow(ctx, `SELECT balance FROM gram_balances WHERE user_id=$1`, userID).Scan(&balance); err != nil {
		return domain.GramBalance{}, fmt.Errorf("grams: credit read: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.GramBalance{}, fmt.Errorf("grams: credit commit: %w", err)
	}
	return domain.GramBalance{UserID: userID, Balance: balance, Granted: true}, nil
}

// Debit 在单事务内做充足性检查后扣款并写流水。
func (s *GramsStore) Debit(ctx context.Context, userID, amount int64, reason string, peer domain.Peer, date int, title, desc string) (domain.GramBalance, error) {
	if s == nil || s.pool == nil || userID <= 0 || amount <= 0 {
		return domain.GramBalance{}, domain.ErrGramsInvalidAmount
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.GramBalance{}, fmt.Errorf("grams: begin debit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var balance int64
	if err := tx.QueryRow(ctx, `SELECT balance FROM gram_balances WHERE user_id=$1 FOR UPDATE`, userID).Scan(&balance); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.GramBalance{}, domain.ErrGramsInsufficient
		}
		return domain.GramBalance{}, fmt.Errorf("grams: debit read: %w", err)
	}
	if balance < amount {
		return domain.GramBalance{}, domain.ErrGramsInsufficient
	}
	if _, err := tx.Exec(ctx, `UPDATE gram_balances SET balance=balance-$1, updated_at=now() WHERE user_id=$2`, amount, userID); err != nil {
		return domain.GramBalance{}, fmt.Errorf("grams: debit balance: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO gram_transactions(user_id,peer_type,peer_id,amount,reason,title,description,date)
VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		userID, peer.Type, peer.ID, -amount, reason, title, desc, date); err != nil {
		return domain.GramBalance{}, fmt.Errorf("grams: debit tx: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.GramBalance{}, fmt.Errorf("grams: debit commit: %w", err)
	}
	return domain.GramBalance{UserID: userID, Balance: balance - amount, Granted: true}, nil
}

// Transfer 在单事务内从发送者转账给接收者。
// amount 是 Gram 单位的整数。
// 返回收到的 GramBalance，或错误。
func (s *GramsStore) Transfer(ctx context.Context, fromUserID, toUserID int64, amount int64) (domain.GramBalance, error) {
	if s == nil || s.pool == nil || fromUserID <= 0 || toUserID <= 0 || amount <= 0 {
		return domain.GramBalance{}, domain.ErrGramsInvalidAmount
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.GramBalance{}, fmt.Errorf("grams: begin transfer tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. 检查发送者余额
	var fromBalance int64
	err = tx.QueryRow(ctx, `SELECT balance FROM gram_balances WHERE user_id=$1 FOR UPDATE`, fromUserID).Scan(&fromBalance)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.GramBalance{}, domain.ErrGramsInsufficient
		}
		return domain.GramBalance{}, fmt.Errorf("grams: transfer read sender: %w", err)
	}
	if fromBalance < amount {
		return domain.GramBalance{}, domain.ErrGramsInsufficient
	}

	// 2. 扣除发送者余额
	newFromBalance := fromBalance - amount
	if _, err := tx.Exec(ctx, `UPDATE gram_balances SET balance = $1, granted = true, updated_at = now() WHERE user_id = $2`, newFromBalance, fromUserID); err != nil {
		return domain.GramBalance{}, fmt.Errorf("grams: transfer debit: %w", err)
	}

	// 3. 给接收者加账 - 查询当前余额
	var toBalance int64
	var newToBalance int64
	err = tx.QueryRow(ctx, `SELECT balance FROM gram_balances WHERE user_id=$1`, toUserID).Scan(&toBalance)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 插入新的余额记录
			if _, err := tx.Exec(ctx, `INSERT INTO gram_balances(user_id,balance,granted,updated_at) VALUES($1,$2,true,now())`, toUserID, amount); err != nil {
				return domain.GramBalance{}, fmt.Errorf("grams: transfer insert recipient: %w", err)
			}
			newToBalance = amount
		} else {
			return domain.GramBalance{}, fmt.Errorf("grams: transfer read recipient: %w", err)
		}
	} else {
		newToBalance = toBalance + amount
		if _, err := tx.Exec(ctx, `UPDATE gram_balances SET balance = $1, granted = true, updated_at = now() WHERE user_id = $2`, newToBalance, toUserID); err != nil {
			return domain.GramBalance{}, fmt.Errorf("grams: transfer credit: %w", err)
		}
	}

	// 4. 写入转账流水
	now := time.Now().Unix()
	if _, err := tx.Exec(ctx, `INSERT INTO gram_transactions (user_id, amount, reason, title, description, date)
		VALUES ($1, $2, 'transfer', 'Gram Transfer', 'Internal transfer', $3)`,
		fromUserID, amount, now); err != nil {
		return domain.GramBalance{}, fmt.Errorf("grams: transfer insert log: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.GramBalance{}, fmt.Errorf("grams: transfer commit: %w", err)
	}

	return domain.GramBalance{UserID: toUserID, Balance: newToBalance, Granted: true}, nil
}

// GetUserAddresses 返回指定用户的钱包地址。
func (s *GramsStore) GetUserAddresses(ctx context.Context, userID int64) (string, error) {
	if s == nil || s.pool == nil || userID <= 0 {
		return "", nil
	}
	var address string
	err := s.pool.QueryRow(ctx, `SELECT address FROM user_wallet_addresses WHERE user_id=$1`, userID).
		Scan(&address)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("grams: get user address: %w", err)
	}
	return address, nil
}
