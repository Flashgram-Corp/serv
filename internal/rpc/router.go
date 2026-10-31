package rpc

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/iamxvbaba/td/bin"
	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/proto"
	"github.com/iamxvbaba/td/tg"

	"github.com/iamxvbaba/td/tlprofile"
	compatandroid "telesrv/internal/compat/android"
	"telesrv/internal/domain"
	"telesrv/internal/links"
	"telesrv/internal/observability/dbtrace"
)

// maxWrapperDepth 限制 invokeWithLayer/initConnection/invokeAfter 等 wrapper 的嵌套深度，防御恶意构造。
// 合法客户端的最深包装来自 gotd 数据连接模式的握手初始化：
// invokeWithLayer → invokeWithoutUpdates → initConnection → invokeWithoutUpdates → query（深度 5），
// 官方服务器同样接受。取 8 留余量，仍是小常量、不削弱对无界嵌套的防御。
const maxWrapperDepth = 8

const maxInvokeAfterMsgIDs = 128

// maxAuthInfoEntries 认证信息缓存最大条目数
const maxAuthInfoEntries = 1 << 16

// tempResolveResult 是 effectiveAuthKeyID 里 temp→perm 解析经 singleflight 共享的结果。
type tempResolveResult struct {
	perm [8]byte
	ok   bool
}

const (
	authKeyResolveSingleflightPrefix    = "resolve:"
	authClientInfoSingleflightPrefix    = "authinfo:"
	authKeyClientInfoSingleflightPrefix = "authkeyinfo:"
)

var (
	tlTypeNamesOnce sync.Once
	tlTypeNames     map[uint32]string
)

// Config 是 Router 所需的服务端信息。
type Config struct {
	DC                  int
	DefaultCountryCode  string // help.getNearestDc 返回的 ISO 3166-1 alpha-2 国家码。
	IP                  string // 对外公布的 DC IP（写入 DCOptions）
	Port                int    // 对外公布的 DC 端口
	InstanceID          string // 进程内唯一标识，用于跨实例 ephemeral push 去重。
	OutboundPushTimeout time.Duration
	SendRateLimit       int
	SendRateWindow      time.Duration
	// AuthCode*RateLimit protects the unauthenticated sendCode/resendCode write path.
	// The phone budget is keyed by SHA-256(normalized phone), never by the plaintext phone;
	// the second budget is keyed by the physical connection's raw auth_key_id.
	AuthCodePhoneRateLimit   int
	AuthCodeAuthKeyRateLimit int
	AuthCodeRateWindow       time.Duration
	// CatchupRateLimit/CatchupRateWindow 限制 difference 类 catch-up RPC（getChannelDifference /
	// getPeerDialogs）的每用户频率（设计 Phase 2 / §10.3）：nudge 被消费后客户端会触发这两类
	// catch-up，放开大群 nudge 全速前需 FLOOD_WAIT 兜底防风暴打爆 PG。两类各自独立计数、共用同一
	// 阈值。<=0 关闭（默认行为不变）。
	CatchupRateLimit  int
	CatchupRateWindow time.Duration
	// SRPRateLimit bounds 2FA password verification RPCs per user (per auth key while pre-auth); <=0 disables.
	SRPRateLimit int
	// SRPRateWindow is the sliding window for SRPRateLimit.
	SRPRateWindow time.Duration
	// WithdrawalRateLimit bounds star-gift and Stars revenue withdrawal RPCs per user; <=0 disables.
	WithdrawalRateLimit int
	// WithdrawalRateWindow is the sliding window for WithdrawalRateLimit.
	WithdrawalRateWindow time.Duration
	// PaymentRateLimit bounds money-moving payments.* RPCs (forms, transfers, offers, bids, upgrades, crafting) per user; <=0 disables.
	PaymentRateLimit int
	// PaymentRateWindow is the sliding window for PaymentRateLimit.
	PaymentRateWindow time.Duration
	// ChannelNudgeMaxTargets 是一次 fan-out 的 >cap nudge 目标上限（设计 Phase 0b 限速兜底）；
	// <=0 用内置默认 defaultChannelNudgeMaxTargets。
	ChannelNudgeMaxTargets int
	// CallSignalingMaxBytes 是 phone.sendSignalingData 单条载荷上限；<=0 不限制。
	CallSignalingMaxBytes int
	// CallForceRelay 强制私聊通话 p2p_allowed=false（调试 TURN 中继路径）。
	// 注意：P2P 默认即安全收紧为仅限互为联系人（phone_turn.go 互认联系人硬门槛），
	// 此开关仅用于全量回退 TURN 的调试/兜底。
	CallForceRelay bool
	// GroupCallMaxParticipants 是群通话单房间参与者上限；<=0 不限制。
	GroupCallMaxParticipants int
	// RtmpIngestURL 是 getGroupCallStreamRtmpUrl 返回给推流端（OBS）的服务器地址，
	// 形如 "rtmp://<host>:<port>/live"。为空时回落 "rtmp://<AdvertiseIP>:2400/live"。
	RtmpIngestURL string
	// PublicBaseURL 是所有客户端可见 telesrv 链接的公开根 URL。
	PublicBaseURL string
	// AllowDevPayments serves the local dev/fiat checkout for Stars and Premium
	// purchases. Denied by default; when false no dev payment form is issued and
	// the telesrv_dev checkout path rejects, so production never mints Stars or
	// Premium without a real XTR payment.
	AllowDevPayments bool
	// UpdatePublicURL is advertised as help.getConfig.autoupdate_url_prefix。
	// Empty keeps the native desktop updater disabled.
	UpdatePublicURL string
	// PublicAppScheme/PublicAppLinkBase 控制客户端 deep link；base 为空时
	// 保持 <scheme>://<route>，非空时生成 <base>/<route>。
	PublicAppScheme   string
	PublicAppLinkBase string
	// TempKeyResolveCacheTTL 是 PFS temp→perm auth key 解析的进程内缓存有效期。>0 时同一 temp key
	// 在 TTL 内复用上次解析、跳过每帧 ResolveAuthKey 的 PG 查询；0（默认/测试）关闭=每帧重校验。
	// 显式撤销会删除协议 auth key、清缓存并断开活跃连接；TTL 只影响自然过期或异常路径下的
	// 下一次重新解析。bind success replaces the exact entry with the committed mapping.
	TempKeyResolveCacheTTL time.Duration
	// TempKeyResolveCacheMaxEntries 是 temp→perm 解析缓存容量；<=0 用内置默认。
	TempKeyResolveCacheMaxEntries int
	// PeerIdentityCacheMaxEntries bounds the viewer-independent peer decoration
	// cache (username vectors and third-party verification marks). Values are
	// validated by the durable peer_identity token; this is not a permission cache.
	PeerIdentityCacheMaxEntries int
	// StoryActivePeerCacheMaxEntries bounds the shared viewer-independent
	// active-story candidate cache. Hidden preferences are one sparse set per
	// viewer and have independent entry/byte bounds.
	StoryActivePeerCacheMaxEntries int
	StoryHiddenListCacheMaxEntries int
	StoryHiddenListCacheMaxBytes   int64
	// PresenceLastSeenBatch* bounds the asynchronous server-lifecycle
	// last-seen writer. Explicit account.updateStatus remains synchronous.
	PresenceLastSeenBatchMax     int
	PresenceLastSeenBatchWait    time.Duration
	PresenceLastSeenBatchQueue   int
	PresenceLastSeenBatchTimeout time.Duration
	PresenceLastSeenDrainTimeout time.Duration
}

func init() {
}

// Router 把解密后的 RPC 请求按 semantic method 路由到 typed handler（tlprofile.Dispatcher）。
//
// handler 输入输出均为 gotd/td/tg 类型，各业务域的 handler
// 与注册见 help.go / auth.go / users.go / updates.go。Router 本身只负责协议外壳：
// 剥离 invokeWithLayer / initConnection / invokeWithoutUpdates / invokeAfter*，并兜底未注册 RPC。
type Router struct {
	cfg               Config
	appLinks          links.AppLinkBuilder
	log               *zap.Logger
	clock             clock.Clock
	deps              Deps
	dispatcher        *tlprofile.Dispatcher
	clientInfoMu      sync.RWMutex
	clientInfo        map[clientInfoSessionKey]clientSessionInfo
	authInfo          map[[8]byte]clientSessionInfo
	authLayerEvidence map[[8]byte]authLayerDefaultEvidence
	authLayerCommit   [authLayerCommitStripes]sync.Mutex
	// authLayerSafeEvictionFloor is the monotonic lower bound supplied by the
	// edge-owned exact admission tracker. Evidence below this sequence has no
	// live owner that can still enter publication and is therefore capacity-
	// evictable. clientInfoMu protects it together with authLayerEvidence.
	authLayerSafeEvictionFloor uint64
	exactProfileMu             sync.RWMutex
	exactProfiles              map[clientInfoSessionKey]exactSessionProfileEntry
	// exactProfileEarliestExpiry is a conservative lower bound. Refresh/deletes
	// may leave it earlier than the true minimum (safe); a capacity scan
	// recomputes it exactly. This makes the normal all-live full-capacity reject
	// O(1) without a heap on every refresh.
	exactProfileEarliestExpiry time.Time
	authUserMu                 sync.RWMutex
	authUsers                  map[[8]byte]authUserCacheEntry
	authUserSF                 singleflight.Group
	mediaCountSF               singleflight.Group
	dialogsPinnedSF            singleflight.Group
	dialogsPinnedListSF        singleflight.Group
	channelFullBotSF           singleflight.Group
	presence                   *presenceTracker
	callbacks                  *callbackRegistry
	preCheckouts               *preCheckoutRegistry
	inlines                    *inlineRegistry
	webviews                   *webViewRegistry
	loginTokens                *loginTokenRegistry
	botAPIUpdates              *botAPIUpdateNotifier
	instanceID                 string
	channelFanout              *channelFanoutDispatcher
	// botAPIEnqueueQueue 把 user→bot 私聊消息的 bot_api_updates 写入移出发送者 RPC 同步
	// 路径（性能审计 H2）；队列满同步回退，绝不丢（队列行是 Bot API 投递真值）。
	botAPIEnqueueQueue *botAPIEnqueueDispatcher
	// presenceCandidateCache 缓存 presence fan-out 的候选 peer 集合（联系人 ∪ 私聊对端，
	// online 过滤前），按 userID 短 TTL；零值 sync.Map 即可用，无需构造器初始化。候选集变动
	// 很慢（加好友/开新私聊），短 TTL 内复用避免 updateStatus 每次重跑 ~25-30 条 hydration 查询。
	presenceCandidateCache sync.Map // userID(int64) -> *presenceCandidateEntry
	// botStatus 永久缓存 userID->是否 bot。bot 标志按账号不可变（BotFather 注册即定，普通用户永不变 bot），
	// 故可无 TTL 缓存。userIsBot 在 PFS 连接上被 announceSessionOnline 每 RPC 调用，不缓存则每次一发
	// Users.ByID 重投影——开群洪峰 ~50 并发时退化成 ~300ms herd（既拖尾延迟也飙 PG CPU）。零值即可用。
	botStatus sync.Map // userID(int64) -> bool
	// lastSeenPersist 记录每个 user 最近一次 last_seen 落库时刻（unix），用于写去抖：
	// updateStatus 高频续期时数秒内只落一次 DB。
	lastSeenPersist sync.Map // userID(int64) -> int64(unix)
	lastSeenBatch   *presenceLastSeenBatchDispatcher
	// tempKeyResolveCache 缓存 rawTempKeyID -> resolved perm（带过期），容量有界。
	tempKeyResolveCache          *tempKeyResolveCache
	storyProjectionCache         *storyProjectionCache
	storySparseProjectionCache   *storySparseProjectionCache
	storyPinnedCache             *storyPinnedAvailableCache
	storyPinnedListCache         *storyPinnedStoriesCache
	channelFullBotCache          *channelFullBotInfoCache
	userFullProjectionCache      *userFullProjectionCache
	peerSettingsProjectionCache  *peerSettingsProjectionCache
	channelFullProjectionCache   *channelFullProjectionCache
	peerIdentityCache            *peerIdentityCache
	availableReactionDocuments   availableReactionDocumentMapCache
	emojiStickers                *emojiStickerIndex
	notifySettings               *notifySettingsCache
	stickerCatalog               *stickerCatalogCache
	transientPrivateBigReactions transientPrivateBigReactionCache
	accountSettings              *accountSettingsCache
	accountFreezeWake            chan struct{}
	accountDeletionWake          chan struct{}

	// frozenPresence 记录因账号冻结而不得向任何对端显示/广播在线的 userID。
	// freeze/unfreeze 由 admin hook 与 durable 通知 worker 即时维护；会话首次绑定时
	// 用 durable fact 补种（seedFrozenPresence），覆盖重启/漏消息后旧在线水位外泄。
	// 零值 sync.Map 即可用。值仅为占位，存在即冻结。
	frozenPresence sync.Map // userID(int64) -> struct{}
	// webPageResolveSem 是链接预览异步解析的并发信号量（有界）：发送后把 pending 占位
	// 解析为卡片并就地替换。满则丢弃任务（消息留 pending）。nil=未启用（测试可直接调
	// resolvePendingWebPage 同步验证）。
	webPageResolveSem chan struct{}
	// selfPhotoEchoPushDelay 是头像变更后向当前 session 回显 updateUser 的延迟
	// （见 photos.go pushSelfPhotoUpdateToCurrentSession）；<=0 时同步推送（测试用）。
	selfPhotoEchoPushDelay time.Duration
}

type clientInfoSessionKey struct {
	rawAuthKeyID [8]byte
	sessionID    int64
}

type clientSessionInfo struct {
	layer int
	// layerObservationID is the durable cross-restart ordering token from
	// auth_keys. It is independent from process-local admission sequence.
	layerObservationID int64
	// layerAdmissionSeq is the server-assigned admission order of explicit
	// invokeWithLayer evidence. It is retained on auth-key defaults and on the
	// exact raw-session metadata shadow written by admission publication. Client
	// msg_id is deliberately not used because its ordering is session-local.
	layerAdmissionSeq      uint64
	wrapperMsgID           int64
	clientInfoAdmissionSeq uint64
	clientInfo             ClientInfo
	hasClientInfo          bool
	authKeyInfoChecked     bool
	authorizationChecked   bool
	layerBlocked           bool
	layerBlockedByAuthKey  bool
}

type exactSessionProfileEntry struct {
	layer         int
	msgID         int64
	observationID int64
	expiresAt     time.Time
}

// ExactSessionProfileCapacityError reports that the bounded replay/profile
// registry contains only still-live logical sessions. Callers must fail closed
// for shared profile mutation; no unexpired msg_id watermark was evicted.
// The marker method lets mtprotoedge recognize the condition without importing
// the higher-level rpc package.
type ExactSessionProfileCapacityError struct {
	Limit int
}

func (e *ExactSessionProfileCapacityError) Error() string {
	return fmt.Sprintf("exact session profile capacity exhausted (limit %d)", e.Limit)
}

func (*ExactSessionProfileCapacityError) ExactSessionProfileCapacity() {}

type authLayerDefaultEvidence struct {
	layer    int
	sequence uint64
	durable  bool
}

type authUserCacheEntry struct {
	userID int64
	found  bool
}

// New 创建 Router，由各业务域自行注册其 RPC handler（registerHelp/Auth/Users/Updates）。
func New(cfg Config, deps Deps, log *zap.Logger, clk clock.Clock) *Router {
	assertNoTypedNilDeps(deps)
	appLinks, err := links.NewAppLinkBuilder(cfg.PublicAppScheme, cfg.PublicAppLinkBase)
	if err != nil {
		panic(fmt.Sprintf("initialize public app links: %v", err))
	}
	instanceID := cfg.InstanceID
	if instanceID == "" {
		instanceID = fmt.Sprintf("%016x", randomNonZeroInt64())
	}
	r := &Router{cfg: cfg, appLinks: appLinks, log: log, clock: clk, deps: deps, exactProfiles: make(map[clientInfoSessionKey]exactSessionProfileEntry), authLayerEvidence: make(map[[8]byte]authLayerDefaultEvidence), presence: newPresenceTracker(), callbacks: newCallbackRegistry(deps.BotCallbacks), preCheckouts: newPreCheckoutRegistry(), inlines: newInlineRegistry(botInlineQueryTTL, deps.Inline), webviews: newWebViewRegistry(webViewSessionTTL, deps.Inline), loginTokens: newLoginTokenRegistry(), botAPIUpdates: newBotAPIUpdateNotifier(), tempKeyResolveCache: newTempKeyResolveCache(cfg.TempKeyResolveCacheMaxEntries), storyProjectionCache: newStoryProjectionCache(clk.Now), storySparseProjectionCache: newStorySparseProjectionCache(deps.ReadModelVersions, cfg.StoryActivePeerCacheMaxEntries, cfg.StoryHiddenListCacheMaxEntries, cfg.StoryHiddenListCacheMaxBytes), storyPinnedCache: newStoryPinnedAvailableCache(clk.Now), storyPinnedListCache: newStoryPinnedStoriesCache(clk.Now), channelFullBotCache: newChannelFullBotInfoCache(clk.Now), userFullProjectionCache: newUserFullProjectionCache(clk.Now), peerSettingsProjectionCache: newPeerSettingsProjectionCache(clk.Now), channelFullProjectionCache: newChannelFullProjectionCache(clk.Now), peerIdentityCache: newPeerIdentityCache(cfg.PeerIdentityCacheMaxEntries), emojiStickers: newEmojiStickerIndex(clk.Now), notifySettings: newNotifySettingsCache(clk.Now), stickerCatalog: newStickerCatalogCache(clk.Now), accountSettings: newAccountSettingsCache(clk.Now), accountFreezeWake: make(chan struct{}, 1), accountDeletionWake: make(chan struct{}, 1), instanceID: instanceID}
	r.channelFanout = newChannelFanoutDispatcher(r, defaultChannelFanoutShards, defaultChannelFanoutBuffer)
	r.botAPIEnqueueQueue = newBotAPIEnqueueDispatcher(log, defaultBotAPIEnqueueBuffer)
	// Zero-valued hand-built Router configs are used by focused unit tests and
	// retain the direct fake-store path. Validated production config always
	// supplies positive batch bounds, so the real server cannot disable this
	// writer or fall back to per-account lifecycle transactions.
	batchConfigured := cfg.PresenceLastSeenBatchMax > 0 || cfg.PresenceLastSeenBatchWait > 0 ||
		cfg.PresenceLastSeenBatchQueue > 0 || cfg.PresenceLastSeenBatchTimeout > 0 ||
		cfg.PresenceLastSeenDrainTimeout > 0
	if updater, ok := deps.Users.(presenceLastSeenBatchUpdater); ok && batchConfigured {
		r.lastSeenBatch = newPresenceLastSeenBatchDispatcher(updater, presenceLastSeenBatchConfig{
			MaxSize: cfg.PresenceLastSeenBatchMax, MaxWait: cfg.PresenceLastSeenBatchWait,
			QueueSize: cfg.PresenceLastSeenBatchQueue, QueryTimeout: cfg.PresenceLastSeenBatchTimeout,
			DrainTimeout: cfg.PresenceLastSeenDrainTimeout,
		}, log, r.metrics())
	}
	r.webPageResolveSem = make(chan struct{}, webPageResolveConcurrency)
	r.selfPhotoEchoPushDelay = defaultSelfPhotoEchoPushDelay
	if cfg.DC > 0 {
		groupCallStreamDCID = cfg.DC
	}
	d := tlprofile.NewDispatcher()
	if err := registerLayerRPCAdmissionFieldPreflights(d); err != nil {
		panic(fmt.Sprintf("register exact layer RPC admission policy: %v", err))
	}
	r.registerAndroidLayerRPCAdapter(d)
	d.OnWrappers(r.consumeLayerRPCWrappers)

	r.registerHelp(d)
	r.registerAuth(d)
	r.registerUsers(d)
	r.registerUpdates(d)
	r.registerAccount(d)
	r.registerMessages(d)
	r.registerStickers(d)
	r.registerChannels(d)
	r.registerCommunities(d)
	r.registerUpload(d)
	r.registerPhotos(d)
	r.registerFolders(d)
	r.registerChatlists(d)
	r.registerContacts(d)
	r.registerLangpack(d)
	r.registerStories(d)
	r.registerPhone(d)
	r.registerEncrypted(d)
	r.registerPayments(d)

	r.dispatcher = d
	return r
}

// assertNoTypedNilDeps rejects partially constructed optional dependencies at
// the composition boundary. A Go interface containing a nil concrete pointer
// the composition boundary. A Go interface containing a nil concrete pointer
// is not equal to nil, so handler-level availability checks would otherwise
// admit it and panic only when the first method is invoked.
func assertNoTypedNilDeps(deps interface{}) {
	value := reflect.ValueOf(deps)
	typeOfDeps := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() != reflect.Interface || field.IsNil() {
			continue
		}
		implementation := field.Elem()
		switch implementation.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			if implementation.IsNil() {
				panic(fmt.Sprintf("rpc: dependency %s is a typed nil %s", typeOfDeps.Field(i).Name, implementation.Type()))
			}
		}
	}
}

// registerHelp 注册帮助 RPC handler。
func registerHelp(d *tlprofile.Dispatcher) { /* ... */ }

// registerAuth 注册认证 RPC handler。
func registerAuth(d *tlprofile.Dispatcher) { /* ... */ }

// registerUsers 注册用户 RPC handler。
func registerUsers(d *tlprofile.Dispatcher) { /* ... */ }

// registerUpdates 注册更新 RPC handler。
func registerUpdates(d *tlprofile.Dispatcher) { /* ... */ }

// registerAccount 注册账户 RPC handler。
func registerAccount(d *tlprofile.Dispatcher) { /* ... */ }

// registerMessages 注册消息 RPC handler。
func registerMessages(d *tlprofile.Dispatcher) { /* ... */ }

// registerStickers 注册贴纸 RPC handler。
func registerStickers(d *tlprofile.Dispatcher) { /* ... */ }

// registerChannels 注册频道 RPC handler。
func registerChannels(d *tlprofile.Dispatcher) { /* ... */ }

// registerCommunities 注册社区 RPC handler。
func registerCommunities(d *tlprofile.Dispatcher) { /* ... */ }

// registerUpload 注册上传 RPC handler。
func registerUpload(d *tlprofile.Dispatcher) { /* ... */ }

// registerPhotos 注册照片 RPC handler。
func registerPhotos(d *tlprofile.Dispatcher) { /* ... */ }

// registerFolders 注册文件夹 RPC handler。
func registerFolders(d *tlprofile.Dispatcher) { /* ... */ }

// registerChatlists 注册聊天列表 RPC handler。
func registerChatlists(d *tlprofile.Dispatcher) { /* ... */ }

// registerContacts 注册联系人 RPC handler。
func registerContacts(d *tlprofile.Dispatcher) { /* ... */ }

// registerLangpack 注册语言包 RPC handler。
func registerLangpack(d *tlprofile.Dispatcher) { /* ... */ }

// registerStories 注册故事 RPC handler。
func registerStories(d *tlprofile.Dispatcher) { /* ... */ }

// registerPhone 注册电话 RPC handler。
func registerPhone(d *tlprofile.Dispatcher) { /* ... */ }

// registerEncrypted 注册加密 RPC handler。
func registerEncrypted(d *tlprofile.Dispatcher) { /* ... */ }

// registerPayments 注册支付 RPC handler。
func registerPayments(d *tlprofile.Dispatcher) { /* ... */ }

func (o *rawObject) Encode(b *bin.Buffer) error {
	b.Put(o.data)
	return nil
}

// rawObject is a raw MTProto object implementation.
type rawObject struct {
	data []byte
}

// registerRPC 注册带语义ID的 RPC handler。
// T 是请求类型，必须实现 bin.Object 接口。
func registerRPC[T bin.Object](d *tlprofile.Dispatcher, method tlprofile.SemanticID, handler func(context.Context, T) (any, error)) {
	if d == nil || handler == nil {
		panic("rpc: register nil canonical RPC handler or dispatcher")
	}
	err := d.Register(method, func(ctx context.Context, object bin.Object) (any, error) {
		request, ok := object.(T)
		if !ok {
			return nil, fmt.Errorf("rpc: semantic %#016x decoded unexpected canonical request %T", uint64(method), object)
		}
		return handler(ctx, request)
	})
	if err != nil {
		panic(fmt.Sprintf("rpc: register canonical RPC %#016x: %v", uint64(method), err))
	}
}