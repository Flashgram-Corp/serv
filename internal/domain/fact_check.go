package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
)

// FactCheck 是事实核查标签（factCheck）。该构造器与 messages.editFactCheck /
// messages.deleteFactCheck / messages.getFactCheck 在 Layer 225 起就已存在于协议里，
// 只是此前没有服务端实现。
//
// 客户端只在广播频道（broadcast channel）的媒体消息上提供编辑入口，且入口是否
// 出现由 app config 的 can_edit_factcheck 全局开关决定；具体能不能写由服务端按
// 频道权限裁决。标签挂在消息本身，因此同一消息在所有订阅者视角下是同一份事实。
type FactCheck struct {
	// NeedCheck 表示该消息尚未核查（客户端渲染为“待核查”状态）。
	NeedCheck bool
	// Country 是核查机构所在国家，ISO 3166-1 alpha-2；未知时为空。
	Country string
	// Text 是核查结论文本。
	Text string
	// Entities 是 Text 的富文本实体（text_url/mention/...）。
	Entities []MessageEntity
	// Hash 是稳定标识：同一条消息的同一份结论文本始终得到同一个 hash，
	// 客户端用它识别“这条事实核查是不是已经改过”。
	Hash int64
}

// MaxFactCheckTextLength 是核查结论的字符上限，与客户端
// factcheck_length_limit 的默认值一致。
const MaxFactCheckTextLength = 1024

// ValidateFactCheck 校验一份事实核查标签是否可以落库。
// 空文本表示删除，不允许作为保存动作。
func ValidateFactCheck(check FactCheck) error {
	if strings.TrimSpace(check.Text) == "" {
		return ErrFactCheckInvalid
	}
	if len([]rune(check.Text)) > MaxFactCheckTextLength {
		return ErrFactCheckTooLong
	}
	if check.Country != "" && !isAlpha2Country(check.Country) {
		return ErrFactCheckInvalid
	}
	return nil
}

func isAlpha2Country(country string) bool {
	if len(country) != 2 {
		return false
	}
	for _, r := range country {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// FactCheckHash 计算事实核查的稳定标识：绑定频道、消息与结论文本，
// 同一份内容永远得到同一个值，改写文本则得到新值。
func FactCheckHash(channelID int64, messageID int, text string) int64 {
	payload := fmt.Sprintf("%d:%d:%s", channelID, messageID, text)
	sum := sha256.Sum256([]byte(payload))
	hash := int64(binary.BigEndian.Uint64(sum[:8]) &^ (1 << 63))
	if hash == 0 {
		hash = 1
	}
	return hash
}

// EditFactCheckRequest 是写一条事实核查的业务请求。
type EditFactCheckRequest struct {
	UserID    int64
	ChannelID int64
	MessageID int
	// Text 为空表示删除事实核查。
	Text      string
	Entities  []MessageEntity
	NeedCheck bool
	Country   string
	Date      int
}

// EditFactCheckResult 返回写完之后的消息快照与标签。
type EditFactCheckResult struct {
	Channel    Channel
	Message    ChannelMessage
	Event      ChannelUpdateEvent
	FactCheck  *FactCheck
	Recipients []int64
}

// FactCheckList 是 messages.getFactCheck 的返回集合，顺序与请求一致。
type FactCheckList struct {
	FactChecks []FactCheck
}
