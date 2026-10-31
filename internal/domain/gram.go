package domain

import "errors"

// Grams 本地账本领域模型（无 TL 类型，镜像 stars.go 风格）。
// 余额为整数 Grams，借记原子、永不为负。

// GramBalance 是一个账号的当前可用 Grams 余额。
type GramBalance struct {
	UserID  int64
	Balance int64
	Granted bool
}

// GramTransaction 是一条 Grams 流水。
type GramTransaction struct {
	ID          int64
	UserID      int64
	Peer        Peer
	Amount      int64
	Reason      string
	Title       string
	Description string
	Date        int
}

// GramTransactionPage 是一页 Grams 流水。
type GramTransactionPage struct {
	Balance      int64
	Transactions []GramTransaction
	NextOffset   int64
	Users        []User
}

// Grams 账本边界常量。
const (
	// DefaultGramsStartingGrant 是惰性首读授予的起始 Grams 余额。
	DefaultGramsStartingGrant = 0
	// MaxGramsTransactionsLimit 是 getGramTransactions 单页上限。
	MaxGramsTransactionsLimit = 100
)

// Grams 账本哨兵错误。
var (
	// ErrGramsInvalidAmount 表示金额非法（<=0）。
	ErrGramsInvalidAmount = errors.New("grams: invalid amount")
	// ErrGramsInsufficient 表示余额不足以完成借记。
	ErrGramsInsufficient = errors.New("grams: insufficient balance")
)
