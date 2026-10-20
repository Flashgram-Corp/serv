package memory

import (
	"context"
	"strings"

	"telesrv/internal/domain"
)

// EditFactCheck 写入或删除一条消息的事实核查标签。
// 权限与频道消息编辑一致：作者本人或具备 edit_messages 权限的管理员。
func (s *ChannelStore) EditFactCheck(_ context.Context, req domain.EditFactCheckRequest) (domain.EditFactCheckResult, error) {
	if req.UserID == 0 || req.ChannelID == 0 || req.MessageID <= 0 {
		return domain.EditFactCheckResult{}, domain.ErrChannelInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	channel, err := s.channelForMemberLocked(req.UserID, req.ChannelID)
	if err != nil {
		return domain.EditFactCheckResult{}, err
	}
	member := s.members[req.ChannelID][req.UserID]
	idx, ok := s.findMessageIndexLocked(req.ChannelID, req.MessageID)
	if !ok || s.messages[req.ChannelID][idx].Deleted || s.messages[req.ChannelID][idx].Action != nil {
		return domain.EditFactCheckResult{}, domain.ErrMessageIDInvalid
	}
	msg := s.messages[req.ChannelID][idx]
	if msg.SenderUserID != req.UserID && !canEditChannelMessage(member) {
		return domain.EditFactCheckResult{}, domain.ErrFactCheckForbidden
	}
	var check *domain.FactCheck
	if strings.TrimSpace(req.Text) != "" {
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
	date := req.Date
	if date == 0 {
		date = msg.Date
	}
	pts := s.nextChannelPtsLocked(req.ChannelID)
	if check == nil {
		msg.FactCheck = nil
	} else {
		copied := *check
		copied.Entities = append([]domain.MessageEntity(nil), check.Entities...)
		msg.FactCheck = &copied
	}
	msg.Pts = pts
	s.messages[req.ChannelID][idx] = msg
	channel.Pts = pts
	s.channels[req.ChannelID] = channel
	event := domain.ChannelUpdateEvent{
		ChannelID:    req.ChannelID,
		Type:         domain.ChannelUpdateEditMessage,
		Pts:          pts,
		PtsCount:     1,
		Date:         date,
		Message:      cloneChannelMessage(msg),
		SenderUserID: req.UserID,
	}
	s.appendChannelEventLocked(event)
	return domain.EditFactCheckResult{
		Channel:    channel,
		Message:    cloneChannelMessage(msg),
		Event:      cloneChannelEvent(event),
		FactCheck:  msg.FactCheck,
		Recipients: s.activeMemberIDsLocked(req.ChannelID, 0, 0),
	}, nil
}

// GetFactChecks 批量读取一组消息的事实核查标签。
func (s *ChannelStore) GetFactChecks(_ context.Context, channelID int64, messageIDs []int) (domain.FactCheckList, error) {
	if channelID == 0 || len(messageIDs) == 0 {
		return domain.FactCheckList{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.FactCheck, 0, len(messageIDs))
	for _, id := range messageIDs {
		idx, ok := s.findMessageIndexLocked(channelID, id)
		if !ok {
			continue
		}
		msg := s.messages[channelID][idx]
		if msg.FactCheck == nil {
			continue
		}
		out = append(out, *msg.FactCheck)
	}
	return domain.FactCheckList{FactChecks: out}, nil
}
