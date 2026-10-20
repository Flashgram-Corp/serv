package postgres

import (
	"context"
	"fmt"
	"strings"

	"telesrv/internal/domain"
)

// EditFactCheck 写入或删除一条消息的事实核查标签。
//
// 权限与频道消息编辑一致：消息作者本人，或频道创作者/具备 edit_messages 权限的
// 管理员。事实核查挂在消息行上，因此所有订阅者读到的是同一份结论。
//
// 写操作占用一个 channel pts 并产生一条 edit_message 事件，客户端据此在原消息
// 上就地更新事实核查标签。
func (s *ChannelStore) EditFactCheck(ctx context.Context, req domain.EditFactCheckRequest) (domain.EditFactCheckResult, error) {
	if req.UserID == 0 || req.ChannelID == 0 || req.MessageID <= 0 {
		return domain.EditFactCheckResult{}, domain.ErrChannelInvalid
	}
	deleting := strings.TrimSpace(req.Text) == ""
	var check *domain.FactCheck
	if !deleting {
		candidate := domain.FactCheck{
			NeedCheck: req.NeedCheck,
			Country:   strings.ToUpper(strings.TrimSpace(req.Country)),
			Text:      req.Text,
			Entities:  req.Entities,
		}
		if err := domain.ValidateFactCheck(candidate); err != nil {
			return domain.EditFactCheckResult{}, err
		}
		candidate.Hash = domain.FactCheckHash(req.ChannelID, req.MessageID, candidate.Text)
		check = &candidate
	}
	if req.Date == 0 {
		req.Date = nowUnix()
	}
	payload, err := encodeFactCheck(check)
	if err != nil {
		return domain.EditFactCheckResult{}, err
	}
	beginner, ok := s.db.(txBeginner)
	if !ok {
		return domain.EditFactCheckResult{}, fmt.Errorf("edit fact check: db does not support transactions")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return domain.EditFactCheckResult{}, fmt.Errorf("begin edit fact check: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	channel, member, err := s.getChannelForMember(ctx, tx, req.UserID, req.ChannelID)
	if err != nil {
		return domain.EditFactCheckResult{}, err
	}
	msg, err := s.getChannelMessage(ctx, tx, req.ChannelID, req.MessageID)
	if err != nil {
		return domain.EditFactCheckResult{}, err
	}
	if msg.Deleted || msg.Action != nil {
		return domain.EditFactCheckResult{}, domain.ErrMessageIDInvalid
	}
	if msg.SenderUserID != req.UserID && !canEditChannelMessage(member) {
		return domain.EditFactCheckResult{}, domain.ErrFactCheckForbidden
	}
	pts, err := s.reserveChannelPts(ctx, tx, req.ChannelID)
	if err != nil {
		return domain.EditFactCheckResult{}, fmt.Errorf("allocate channel fact check pts: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE channel_messages
SET factcheck = $3,
    pts = $4,
    updated_at = now()
WHERE channel_id = $1 AND id = $2 AND NOT deleted`,
		req.ChannelID, req.MessageID, string(payload), pts); err != nil {
		return domain.EditFactCheckResult{}, fmt.Errorf("update channel fact check: %w", err)
	}
	msg.FactCheck = check
	msg.Pts = pts
	event := domain.ChannelUpdateEvent{
		ChannelID:    req.ChannelID,
		Type:         domain.ChannelUpdateEditMessage,
		Pts:          pts,
		PtsCount:     1,
		Date:         req.Date,
		Message:      msg,
		SenderUserID: req.UserID,
	}
	if err := insertChannelEventTx(ctx, tx, event); err != nil {
		return domain.EditFactCheckResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE channels SET pts = $2, updated_at = now() WHERE id = $1`, req.ChannelID, pts); err != nil {
		return domain.EditFactCheckResult{}, fmt.Errorf("update channel fact check pts: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.EditFactCheckResult{}, fmt.Errorf("commit edit fact check: %w", err)
	}
	committed = true
	recipients, _ := s.ListActiveChannelMemberIDs(ctx, req.UserID, req.ChannelID, 0)
	return domain.EditFactCheckResult{Channel: channel, Message: msg, FactCheck: check, Event: event, Recipients: recipients}, nil
}

// GetFactChecks 批量读取一组消息的事实核查标签。
// 没有标签的消息不出现在结果里，因此顺序由请求决定、长度可能更短。
func (s *ChannelStore) GetFactChecks(ctx context.Context, channelID int64, messageIDs []int) (domain.FactCheckList, error) {
	if channelID == 0 || len(messageIDs) == 0 {
		return domain.FactCheckList{}, nil
	}
	rows, err := s.db.Query(ctx, `
SELECT id, factcheck::text
FROM channel_messages
WHERE channel_id = $1 AND id = ANY($2::int[]) AND factcheck <> '{}'::jsonb`,
		channelID, messageIDs)
	if err != nil {
		return domain.FactCheckList{}, fmt.Errorf("read channel fact checks: %w", err)
	}
	defer rows.Close()
	out := make([]domain.FactCheck, 0, len(messageIDs))
	for rows.Next() {
		var id int
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return domain.FactCheckList{}, fmt.Errorf("scan channel fact check: %w", err)
		}
		check, err := decodeFactCheck(raw)
		if err != nil {
			return domain.FactCheckList{}, err
		}
		if check == nil {
			continue
		}
		out = append(out, *check)
	}
	if err := rows.Err(); err != nil {
		return domain.FactCheckList{}, fmt.Errorf("read channel fact checks: %w", err)
	}
	return domain.FactCheckList{FactChecks: out}, nil
}
