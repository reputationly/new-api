package model

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// ModerationLog 内容审核记录。见 docs/content-moderation-design.md §10。
//
// 这张表存在的首要理由不是「增强」，而是补一个既有盲区（§1.0）：
// 关键词拦截今天不产生任何 DB 记录——拒绝发生在预扣费之前就 return 了，消费日志不会写；
// RecordErrorLog 只在重试循环里被调用，也够不着。运营侧只能看到一行文件日志，
// 里面有命中的词和一个 join 不到任何东西的 requestId。
//
// 遵守 Rule 2（三库兼容）：JSON 存 TEXT 不用 JSONB，主键交给 GORM，避开 key / group 保留字。
type ModerationLog struct {
	Id        int    `json:"id"`
	UserId    int    `json:"user_id" gorm:"index"`
	TokenId   int    `json:"token_id" gorm:"index"`
	ChannelId int    `json:"channel_id" gorm:"index"`
	Username  string `json:"username" gorm:"type:varchar(64);index"`
	// Group 命中时生效的分组。列名用 user_group 避开 PostgreSQL 保留字。
	Group     string `json:"group" gorm:"column:user_group;type:varchar(64);index"`
	Policy    string `json:"policy" gorm:"type:varchar(64)"`
	TaskId    string `json:"task_id" gorm:"type:varchar(64);index"`
	RequestId string `json:"request_id" gorm:"type:varchar(64);index"`
	ModelName string `json:"model_name" gorm:"type:varchar(64);index"`

	Source     string `json:"source" gorm:"type:varchar(16);index"` // self / upstream，见 §9.3
	Stage      string `json:"stage" gorm:"type:varchar(16);index"`  // prompt / input_media（output 预留）
	Modality   string `json:"modality" gorm:"type:varchar(16)"`     // text / image（video / audio 预留）
	Action     string `json:"action" gorm:"type:varchar(16);index"` // pass / block / review / mask / error
	Categories string `json:"categories" gorm:"type:varchar(255)"`  // 逗号分隔；拒绝文案里「原因」的来源（§9.2.2）
	// Enforced 这个判定是否真的执行了。observe 模式下 Action 仍是 block 但请求照常放行，
	// 两者必须分开存：
	//   - 「本周拦了多少」按 action 数会把观察期的误杀一并算进去，得按这一列数；
	//   - 全文留存只对真拦下来的请求成立——正常返回了结果的请求留半年完整 prompt，
	//     数据最小化上说不过去（见 SetModerationContent）。
	Enforced bool `json:"enforced" gorm:"index"`

	Score    float64 `json:"score"`
	Provider string  `json:"provider" gorm:"type:varchar(32)"` // L0 / L1 / L2 / L3
	// Words L0 命中的关键词，换行分隔（见 ModerationWordsSep）。独立成列而不是塞进 Detail：
	// 「这条词拦了多少次、误杀几次」是调词库时唯一真正要问的问题，
	// 埋在 JSON 里就只能全表 LIKE，页面上也筛不了。
	// 只进管理端，绝不回显给用户——那等于送一个免费的绕过探测器（§9.2.2）。
	Words string `json:"words" gorm:"type:varchar(512)"`

	ObjectKey   string `json:"object_key" gorm:"type:varchar(512)"`        // 被拦图片的 OBS key（第二期）
	ContentHash string `json:"content_hash" gorm:"type:varchar(64);index"` // SHA-256(归一化原文)
	Preview     string `json:"preview" gorm:"type:varchar(320)"`           // 原文前 160 字符，pass 记录不写（见 SetModerationContent）
	// ContentEnc AES-256-GCM 密文，仅真正拦下来的请求写入。json tag 必须是 "-"：
	// 结构体绝不能把密文序列化出去，取原文只能走带鉴权和留痕的独立接口（§10.1）。
	//
	// 不能写 type:text——MySQL 的 TEXT 上限 64KiB，而 AES-GCM 加 base64 约 4/3 膨胀，
	// 明文过 49KB 就会静默截断成解不开的东西（非严格模式）或直接插入失败（严格模式）。
	// 而「被拦的长 prompt」正是最该留证的那一类。省略 tag 让 GORM 按库映射
	// （MySQL longtext / PG text / SQLite text），与 model/log.go 的 Other 一致。
	ContentEnc string `json:"-"`
	Detail     string `json:"detail" gorm:"type:text"` // JSON，只含脱敏内容，体量恒定，text 够用

	CreatedAt int64 `json:"created_at" gorm:"index"`

	// HasContent 该记录是否存有可解密原文。非持久化字段，列表接口填充，
	// 前端据此决定是否显示「查看原文」——它拿不到也不该拿到密文本身。
	HasContent bool `json:"has_content" gorm:"-"`
}

func (ModerationLog) TableName() string {
	return "moderation_logs"
}

// 审核动作。与 service/moderation 的 Action 取值一致，此处独立定义避免 model 依赖 service。
const (
	ModerationActionPass   = "pass"
	ModerationActionBlock  = "block"
	ModerationActionReview = "review"
	ModerationActionMask   = "mask"
	ModerationActionError  = "error"
)

const (
	ModerationSourceSelf     = "self"
	ModerationSourceUpstream = "upstream"
)

// ModerationWordsSep words 列的分隔符。
//
// 用换行而不是逗号：敏感词由运营自由填写，词条里可能带逗号，
// 而换行是不可能的——词表本身就是按换行切出来的（setting/sensitive.go:29）。
// Categories 仍用逗号：它的取值是固定枚举，不存在这个问题。
const ModerationWordsSep = "\n"

// previewLimit 内容预览的硬上限（rune）。
//
// 注意这是截断不是脱敏：预览里是原文的前 160 字符。之所以敢这么存，是因为
// 只有 block / review / mask / error 记录才写预览——那些内容本来就是要人看的，
// 看不到就没法判误杀。pass 记录一律不写（见 SetModerationContent），
// 否则按默认 1% 抽样，正常请求的 prompt 头部会明文躺在一张列表接口就能拉出来的表里。
const previewLimit = 160

var (
	moderationQueue     chan *ModerationLog
	moderationQueueOnce sync.Once
	moderationDropped   int64
	moderationDropMu    sync.Mutex

	// moderationKeyWarnOnce 密钥缺失导致原文留不下时告警一次。
	// 不在启动时告警：审核默认关着，多数部署根本用不到这个密钥，
	// 每次启动喷一行 WARNING 的唯一效果是让人学会忽略它。
	moderationKeyWarnOnce sync.Once
)

// InitModerationLogWorker 启动异步落库 worker。
// 审核在关键路径上，落库不能拖慢拒绝响应（§9.2「同步决策，异步副作用」）。
func InitModerationLogWorker() {
	moderationQueueOnce.Do(func() {
		size := system_setting.GetModerationSettings().LogQueueSize
		if size <= 0 {
			size = 2048
		}
		moderationQueue = make(chan *ModerationLog, size)
		go func() {
			for entry := range moderationQueue {
				writeModerationLog(entry)
			}
		}()
	})
}

// writeModerationLog 单条落库。带 recover：这是个独立后台 goroutine，
// 它 panic 不会被任何请求的 recover 接住，会直接把整个网关带走——
// 为了一条审核日志赔上整个进程，这笔账怎么算都不划算。
func writeModerationLog(entry *ModerationLog) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("moderation_log 落库 panic: %v", r))
		}
	}()
	if DB == nil {
		return
	}
	if err := DB.Create(entry).Error; err != nil {
		common.SysError("moderation_log 落库失败: " + err.Error())
	}
}

// RecordModerationLog 异步写一条审核记录。
//
// 队列满时丢日志而不是阻塞请求，但必须计数并告警——静默丢日志会让审核记录出现
// 无法解释的空洞，而「记录里没有」和「没发生过」在事后排查时是分不清的（§9.2）。
func RecordModerationLog(entry *ModerationLog) {
	if entry == nil {
		return
	}
	if entry.CreatedAt == 0 {
		entry.CreatedAt = time.Now().Unix()
	}
	entry.Preview = TruncatePreview(entry.Preview)

	InitModerationLogWorker()
	select {
	case moderationQueue <- entry:
	default:
		moderationDropMu.Lock()
		moderationDropped++
		n := moderationDropped
		moderationDropMu.Unlock()
		// 每丢 100 条报一次，避免队列持续满时把日志刷爆。
		if n%100 == 1 {
			common.SysError("moderation_log 队列已满，已累计丢弃 " + strconv.FormatInt(n, 10) + " 条审核记录")
		}
	}
}

// ModerationDroppedCount 供运行态页面展示丢弃数（§8.4 运行态）。
func ModerationDroppedCount() int64 {
	moderationDropMu.Lock()
	defer moderationDropMu.Unlock()
	return moderationDropped
}

// TruncatePreview 按 rune 硬截断脱敏预览。
// 按字节截断会把多字节字符切成半个，DB 里存进去是乱码，运营看不出误杀。
func TruncatePreview(s string) string {
	runes := []rune(s)
	if len(runes) <= previewLimit {
		return s
	}
	return string(runes[:previewLimit]) + "…"
}

// ShouldSampleModerationPass 决定这条 pass 记录是否落库。
//
// 抽样不能是确定性的：用 hash 取模那类可从外部推算的规则，等于告诉攻击者
// 哪些输入不会被记录（§10）。抽样只影响日志，不影响是否审核——审核永远 100%。
func ShouldSampleModerationPass() bool {
	rate := system_setting.GetModerationSettings().LogPassSampleRate
	if rate <= 0 {
		return false
	}
	if rate >= 1 {
		return true
	}
	return rand.Float64() < rate
}

// SetModerationContent 按 §10.1 的留存策略填充内容三件套。
//
// 三档：pass 什么都不留（只有 hash）；判了但没执行的留预览；真拦下来的才留全文密文。
// 必须在 Action 与 Enforced 都赋值之后调用。
//
// 两个参数各司其职，不能合并：
//   - normalized 只用来算 hash。归一化让「同一内容换个零宽字符」收敛成同一个 hash，
//     §11 的 hash 黑名单和重发判定都指望这个稳定性。
//   - raw 才是留证内容。归一化会转小写、折叠空白、把西里尔同形字换成拉丁字母，
//     存它等于把取证材料换成我们自己改写过的版本，而页面上那一列写的是「原文」。
func (m *ModerationLog) SetModerationContent(raw, normalized string) {
	m.ContentHash = common.HashModerationContent(normalized)

	// pass 记录不留任何内容形态。它的用途是量抽样率和总量，ContentHash 已经够了；
	// 留预览等于把正常用户的 prompt 头部明文存进一张无需审计就能列出来的表。
	if m.Action == ModerationActionPass {
		return
	}
	m.Preview = TruncatePreview(raw)

	// 全文只对「真拦下来」的请求留存，而不是「判定为 block」。
	//
	// observe 模式下判定照出但请求正常返回了结果，把这类完整 prompt 加密留存
	// 180 天，对一个什么都没拦的请求来说是过度留存。代价是观察期只剩 160 字符
	// 预览可看误杀——这是有意的取舍：要看全文就切回 blocking，那时留存才有对价。
	if m.Action != ModerationActionBlock || !m.Enforced {
		return
	}
	enc, err := common.EncryptModerationContent(raw)
	if err != nil {
		// 密钥没配就是没配，ContentEnc 留空即可——写进去一堆解不开的东西
		// 比不写更糟，因为它看起来是「存了」（§10.1）。
		if err == common.ErrModerationKeyMissing {
			// 但要让人知道。这一刻是唯一能证明「确实有原文没留下来」的时点：
			// 事后回头查，看到的只是一堆 ContentEnc 为空的记录，分不清是没配密钥
			// 还是本来就没内容，而那时想补也补不回来了。
			moderationKeyWarnOnce.Do(func() {
				common.SysError("MODERATION_ENCRYPT_KEY 未配置，拦截记录的原文无法留存，事后无法复核（此告警仅提示一次）")
			})
			return
		}
		common.SysError("moderation_log 原文加密失败: " + err.Error())
		return
	}
	m.ContentEnc = enc
}

// CleanupModerationLogs 按 §10 分档清理：Block/Review/error 留 180 天，Pass 留 3 天。
// 不做这个区分的话，observe 模式下的全量 Pass 会在一周内把表撑到不可维护。
func CleanupModerationLogs() error {
	s := system_setting.GetModerationSettings()
	blockDays := s.RetentionBlockDays
	if blockDays <= 0 {
		// 兜底值必须和 system_setting 的默认值一致（180 天 / 六个月，对齐备案口径）。
		// 两处不一致的话，配置项被清空时会静默按更短的档删——而删除不可逆。
		blockDays = 180
	}
	passDays := s.RetentionPassDays
	if passDays <= 0 {
		passDays = 3
	}
	now := time.Now()
	passCutoff := now.AddDate(0, 0, -passDays).Unix()
	blockCutoff := now.AddDate(0, 0, -blockDays).Unix()

	if err := DB.Where("action = ? AND created_at < ?", ModerationActionPass, passCutoff).
		Delete(&ModerationLog{}).Error; err != nil {
		return err
	}
	return DB.Where("action <> ? AND created_at < ?", ModerationActionPass, blockCutoff).
		Delete(&ModerationLog{}).Error
}

// ModerationLogQuery 审核记录的筛选条件。零值字段表示不过滤。
type ModerationLogQuery struct {
	StartTimestamp int64
	EndTimestamp   int64
	UserId         int
	Username       string
	Group          string
	ChannelId      int
	ModelName      string
	Action         string
	Source         string
	Category       string
	Word           string
	RequestId      string
	StartIdx       int
	PageSize       int
}

// likeEscapeChar LIKE 模式的转义符。
//
// 不用反斜杠：MySQL 的字符串字面量里 `\` 会把紧跟的引号吃掉，`ESCAPE '\'` 直接是
// 语法错误，得写成 `ESCAPE '\\'`；而 PostgreSQL 在 standard_conforming_strings=on
// 下 `'\\'` 又是两个反斜杠。选一个在三库里都只是普通字符的符号，绕开这个分歧（Rule 2）。
const likeEscapeChar = "!"

// escapeLike 转义 LIKE 模式里的元字符。
//
// 不做这步，whereDelimitedContains 宣称的「精确匹配」在值含 % 或 _ 时就是假的：
// 搜 "100%" 会把任何以 100 开头的词都捞出来，搜 "a_b" 会命中 "axb"。
// 敏感词由运营自由填写，这两个字符都不罕见。
//
// 转义符本身必须先替换，否则值里原有的 "!" 会把后面的字符吞掉。
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, likeEscapeChar, likeEscapeChar+likeEscapeChar)
	s = strings.ReplaceAll(s, "%", likeEscapeChar+"%")
	s = strings.ReplaceAll(s, "_", likeEscapeChar+"_")
	return s
}

// whereDelimitedContains 在分隔符拼接的列里精确匹配一项。
//
// 四个分支分别对应「唯一一项」「第一项」「最后一项」「中间项」。
// 不能只用 LIKE '%x%'：那样查「赌博」会连「反赌博」一起捞出来。
// 拼接放在 Go 侧做，因为 PostgreSQL 的 || 和 MySQL 的 CONCAT 语法不通用（Rule 2）。
//
// sep 必须与写入时用的分隔符一致：categories 用逗号，words 用换行
// （ModerationWordsSep，因为词条可能含逗号）。传错了不会报错，只会一条都查不到。
// sep 自身不转义：`,` 和 `\n` 都不是 LIKE 元字符，且两端的 % 正是我们要的通配。
func whereDelimitedContains(tx *gorm.DB, column, sep, value string) *gorm.DB {
	esc := escapeLike(value)
	like := " LIKE ? ESCAPE '" + likeEscapeChar + "'"
	return tx.Where(
		column+" = ?"+" OR "+column+like+" OR "+column+like+" OR "+column+like,
		value, esc+sep+"%", "%"+sep+esc, "%"+sep+esc+sep+"%")
}

// GetModerationLogs 分页查审核记录。
//
// 返回的结构体不含 ContentEnc（json tag 是 "-"），密文不会随列表接口出去；
// 取原文只能走 GetModerationLogContent 那条带鉴权和留痕的路（§10.1）。
func GetModerationLogs(q ModerationLogQuery) ([]*ModerationLog, int64, error) {
	tx := DB.Model(&ModerationLog{})

	if q.StartTimestamp != 0 {
		tx = tx.Where("created_at >= ?", q.StartTimestamp)
	}
	if q.EndTimestamp != 0 {
		tx = tx.Where("created_at <= ?", q.EndTimestamp)
	}
	if q.UserId != 0 {
		tx = tx.Where("user_id = ?", q.UserId)
	}
	if q.Username != "" {
		tx = tx.Where("username = ?", q.Username)
	}
	if q.Group != "" {
		// user_group 是为绕开 PostgreSQL 保留字才改的列名，这里也必须用它。
		tx = tx.Where("user_group = ?", q.Group)
	}
	if q.ChannelId != 0 {
		tx = tx.Where("channel_id = ?", q.ChannelId)
	}
	if q.ModelName != "" {
		tx = tx.Where("model_name = ?", q.ModelName)
	}
	if q.Action != "" {
		tx = tx.Where("action = ?", q.Action)
	}
	if q.Source != "" {
		tx = tx.Where("source = ?", q.Source)
	}
	if q.RequestId != "" {
		tx = tx.Where("request_id = ?", q.RequestId)
	}
	if q.Category != "" {
		tx = whereDelimitedContains(tx, "categories", ",", q.Category)
	}
	if q.Word != "" {
		tx = whereDelimitedContains(tx, "words", ModerationWordsSep, q.Word)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 上界不能省。GetPageQuery 直接把 ?page_size= 透传进来，不做任何限制，
	// 而这个接口每行都带内容预览——一次 page_size=1000000 就能把几百 MB 预览
	// 拉进内存再吐给一个只需要看几十条的页面。仓库里同类管理端列表都封 100
	// （controller/kyc.go:246、invoice.go:167、feedback.go:363 等），这里对齐。
	const maxPageSize = 100
	pageSize := q.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	// 逐列列出而不是 SELECT *：这个接口的整个设计前提就是它看不到密文，
	// 拉进内存再擦掉不算「看不到」——密文可能有几十上百 KB，一页 50 行就是几 MB
	// 白白穿过网络和 GC，而且任何一次误改（比如把 ContentEnc 的 json tag 改回去）
	// 都会立刻变成泄漏。has_content 用一个轻量表达式在库里算完。
	//
	// 新增列时记得同步这里，否则新列不会出现在列表接口里——漏了是可见的缺字段，
	// 而 SELECT * 漏的是不可见的泄漏，两害相权取其轻。
	rows := make([]*moderationLogListRow, 0, pageSize)
	if err := tx.Select(moderationLogListColumns).
		Order("id desc").Limit(pageSize).Offset(q.StartIdx).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}

	logs := make([]*ModerationLog, 0, len(rows))
	for _, r := range rows {
		entry := r.ModerationLog
		// HasContent 是给前端决定要不要显示「查看原文」按钮用的。
		// 必须由后端算：密文本身 json tag 是 "-"，前端拿不到也不该拿到。
		entry.HasContent = r.HasContentFlag > 0
		logs = append(logs, &entry)
	}
	return logs, total, nil
}

// moderationLogListRow 列表查询的扫描目标。
// 内嵌 ModerationLog 复用字段映射，额外接一列库里算好的 has_content。
type moderationLogListRow struct {
	ModerationLog
	HasContentFlag int64 `gorm:"column:has_content"`
}

// moderationLogListColumns 列表接口的投影：除 content_enc 外的全部列，
// 外加一个「有无密文」的布尔表达式。
//
// NULL 判断不能省：content_enc 可空，历史行可能是 NULL，
// 而 `content_enc <> ”` 对 NULL 求值为 NULL（三库一致），会被当成 false 之外的第三态。
// CASE WHEN 写法三库通用，避开了 PostgreSQL 与 MySQL 的布尔字面量差异（Rule 2）。
const moderationLogListColumns = "id, user_id, token_id, channel_id, username, user_group, " +
	"policy, task_id, request_id, model_name, source, stage, modality, action, categories, " +
	"enforced, score, provider, words, object_key, content_hash, preview, detail, created_at, " +
	"(CASE WHEN content_enc IS NULL OR content_enc = '' THEN 0 ELSE 1 END) AS has_content"

// GetModerationLogContent 解密取原文。调用方必须已完成管理员鉴权，
// 并在调用后写一条管理操作审计（§10.1 访问控制第 2 条）。
func GetModerationLogContent(id int) (string, error) {
	var entry ModerationLog
	if err := DB.Select("content_enc").Where("id = ?", id).First(&entry).Error; err != nil {
		return "", err
	}
	return common.DecryptModerationContent(entry.ContentEnc)
}
