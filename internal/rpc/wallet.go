package rpc

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"telesrv/internal/store/postgres"
	"telesrv/internal/domain"
)

// walletService - реализация операций кошелька TON с базами данных.
type walletService struct {
	gramsStore postgres.GramsStore
	accountID  int64
}

// NewWalletService создает новый сервис кошелька.
func NewWalletService(gs postgres.GramsStore, accountID int64) *walletService {
	return &walletService{
		gramsStore: gs,
		accountID:  accountID,
	}
}

// GetBalance возвращает баланс пользователя.
func (w *walletService) GetBalance(_ context.Context, userID int64) (domain.GramBalance, error) {
	return w.gramsStore.GetBalance(context.Background(), userID)
}

// SendTransaction отправляет транзакцию Gram между пользователями.
func (w *walletService) SendTransaction(_ context.Context, amount *big.Int, destination string, _ string) (string, error) {
	// Интерпретируем amount как целые Грамы
	amountGram := amount.Int64()

	// destination — UserID получателя в виде строки от клиента
	recipientID, err := strconv.ParseInt(destination, 10, 64)
	if err != nil || recipientID <= 0 {
		return "", fmt.Errorf("send transaction: invalid destination userID %s", destination)
	}

	// Нельзя пересылать Gram самому себе
	if recipientID == w.accountID {
		return "", fmt.Errorf("send transaction: cannot send to yourself")
	}

	// Используем Store.Transfer 进行原子转账
	_, err = w.gramsStore.Transfer(context.Background(), w.accountID, recipientID, amountGram)
	if err != nil {
		if errors.Is(err, domain.ErrGramsInsufficient) {
			return "", fmt.Errorf("send transaction: insufficient balance: have %d, need %d",
				amountGram-amountGram, amountGram) // balances already checked
		}
		return "", fmt.Errorf("send transaction: transfer failed: %w", err)
	}

	// Возвращаем уникальный ID транзакции
	return fmt.Sprintf("%d", time.Now().UnixNano()), nil
}