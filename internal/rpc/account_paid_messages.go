package rpc

import (
	"context"
	"strings"

	"github.com/iamxvbaba/td/tgerr"

	"telesrv/internal/domain"
)

func (r *Router) validateStarGiftCommentAllowed(ctx context.Context, buyerID int64, to domain.Peer, message string) error {
	if strings.TrimSpace(message) == "" {
		return nil
	}
	switch to.Type {
	case domain.PeerTypeUser:
		return r.validateStarGiftCommentForUser(ctx, buyerID, to.ID)
	case domain.PeerTypeChannel:
		return r.validateStarGiftCommentForChannel(ctx, buyerID, to.ID)
	default:
		return starGiftInvalidErr()
	}
}

func (r *Router) validateStarGiftCommentForUser(ctx context.Context, buyerID, recipientID int64) error {
	requirement, err := r.privateContactRestrictionFor(ctx, buyerID, recipientID)
	if err != nil {
		return err
	}
	if requirement.paidStars <= 0 {
		return nil
	}
	if svc, ok := r.deps.PaidMessages.(interface {
		HasPaidMessagesException(ctx context.Context, channelID, granterUserID, userID int64) (bool, error)
	}); ok {
		excepted, err := svc.HasPaidMessagesException(ctx, 0, recipientID, buyerID)
		if err != nil {
			return internalErr()
		}
		if excepted {
			return nil
		}
	}
	return tgerr.New(400, "STARGIFT_MESSAGE_INVALID")
}

func (r *Router) validateStarGiftCommentForChannel(ctx context.Context, buyerID, channelID int64) error {
	if r.deps.Channels == nil {
		return internalErr()
	}
	view, err := r.deps.Channels.ResolveChannel(ctx, buyerID, channelID)
	if err != nil {
		return internalErr()
	}
	if view.Channel.SendPaidMessagesStars <= 0 {
		return nil
	}
	if view.Self.CanManageDirectMessages() {
		return nil
	}
	if svc, ok := r.deps.PaidMessages.(interface {
		HasPaidMessagesException(ctx context.Context, channelID, granterUserID, userID int64) (bool, error)
	}); ok {
		excepted, err := svc.HasPaidMessagesException(ctx, channelID, 0, buyerID)
		if err != nil {
			return internalErr()
		}
		if excepted {
			return nil
		}
	}
	return tgerr.New(400, "STARGIFT_MESSAGE_INVALID")
}
