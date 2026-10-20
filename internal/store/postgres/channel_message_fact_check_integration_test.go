package postgres

import (
	"context"
	"errors"
	"testing"

	"telesrv/internal/domain"
)

// TestChannelFactCheckSurvivesReadPaths 验证 Layer 231 事实核查标签
// 存在 channel_messages.factcheck 列里，并且：
//   - 作者可以写入结论，非作者普通成员不行；
//   - 读路径（GetChannelMessages）带回标签；
//   - 批量读取（GetFactChecks）返回同一条结论；
//   - 删除后标签消失。
func TestChannelFactCheckSurvivesReadPaths(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{AccessHash: 91, Phone: "+1997" + suffix + "01", FirstName: "FactOwner"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	member, err := users.Create(ctx, domain.User{AccessHash: 92, Phone: "+1997" + suffix + "02", FirstName: "FactMember"})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	ids := []int64{owner.ID, member.ID}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM channel_update_events WHERE channel_id = ANY($1::bigint[])", []int64{})
		_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = ANY($1::bigint[])", ids)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", ids)
	})

	channels := NewChannelStore(pool)
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "Fact Check " + suffix, Broadcast: true, Date: 1_700_100_000,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID := created.Channel.ID
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM channel_update_events WHERE channel_id = $1", channelID)
		_, _ = pool.Exec(ctx, "DELETE FROM channel_messages WHERE channel_id = $1", channelID)
		_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = $1", channelID)
	})

	post, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner.ID, ChannelID: channelID, RandomID: 1, Message: "post", Date: 1_700_100_100,
	})
	if err != nil {
		t.Fatalf("send post: %v", err)
	}
	if _, err := channels.JoinChannel(ctx, channelID, member.ID, 1_700_100_200); err != nil {
		t.Fatalf("join member: %v", err)
	}

	// 非作者普通成员不能写事实核查。
	if _, err := channels.EditFactCheck(ctx, domain.EditFactCheckRequest{
		UserID: member.ID, ChannelID: channelID, MessageID: post.Message.ID, Text: "not allowed", Date: 1_700_100_300,
	}); !errors.Is(err, domain.ErrFactCheckForbidden) {
		t.Fatalf("member editFactCheck error = %v, want ErrFactCheckForbidden", err)
	}

	// 作者写入结论。
	res, err := channels.EditFactCheck(ctx, domain.EditFactCheckRequest{
		UserID: owner.ID, ChannelID: channelID, MessageID: post.Message.ID,
		Text: "The claim is false.", Country: "DE", Date: 1_700_100_400,
	})
	if err != nil {
		t.Fatalf("editFactCheck: %v", err)
	}
	if res.FactCheck == nil || res.FactCheck.Text != "The claim is false." {
		t.Fatalf("editFactCheck result = %+v", res.FactCheck)
	}
	if res.FactCheck.Hash == 0 {
		t.Fatal("fact check hash is zero")
	}

	// 读路径带回标签。
	history, err := channels.GetChannelMessages(ctx, owner.ID, channelID, []int{post.Message.ID})
	if err != nil {
		t.Fatalf("get channel messages: %v", err)
	}
	if len(history.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(history.Messages))
	}
	got := history.Messages[0].FactCheck
	if got == nil {
		t.Fatal("channel message has no fact check")
	}
	if got.Text != "The claim is false." || got.Country != "DE" {
		t.Fatalf("fact check = %+v", got)
	}
	if got.Hash != res.FactCheck.Hash {
		t.Fatalf("fact check hash = %d, want %d", got.Hash, res.FactCheck.Hash)
	}

	// 批量读取返回同一条结论。
	list, err := channels.GetFactChecks(ctx, channelID, []int{post.Message.ID})
	if err != nil {
		t.Fatalf("getFactChecks: %v", err)
	}
	if len(list.FactChecks) != 1 || list.FactChecks[0].Text != "The claim is false." {
		t.Fatalf("getFactChecks = %+v", list.FactChecks)
	}

	// 删除后标签消失。
	if _, err := channels.EditFactCheck(ctx, domain.EditFactCheckRequest{
		UserID: owner.ID, ChannelID: channelID, MessageID: post.Message.ID, Text: "", Date: 1_700_100_500,
	}); err != nil {
		t.Fatalf("deleteFactCheck: %v", err)
	}
	history, err = channels.GetChannelMessages(ctx, owner.ID, channelID, []int{post.Message.ID})
	if err != nil {
		t.Fatalf("get channel messages after delete: %v", err)
	}
	if history.Messages[0].FactCheck != nil {
		t.Fatalf("message still carries a fact check: %+v", history.Messages[0].FactCheck)
	}
}
