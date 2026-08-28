package model

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var commonGroupCol string
var commonKeyCol string
var commonTrueVal string
var commonFalseVal string

var logKeyCol string
var logGroupCol string

func initCol() {
	// init common column names
	if common.UsingPostgreSQL {
		commonGroupCol = `"group"`
		commonKeyCol = `"key"`
		commonTrueVal = "true"
		commonFalseVal = "false"
	} else {
		commonGroupCol = "`group`"
		commonKeyCol = "`key`"
		commonTrueVal = "1"
		commonFalseVal = "0"
	}
	if os.Getenv("LOG_SQL_DSN") != "" {
		switch common.LogSqlType {
		case common.DatabaseTypePostgreSQL:
			logGroupCol = `"group"`
			logKeyCol = `"key"`
		default:
			logGroupCol = commonGroupCol
			logKeyCol = commonKeyCol
		}
	} else {
		// LOG_SQL_DSN 为空时，日志数据库与主数据库相同
		if common.UsingPostgreSQL {
			logGroupCol = `"group"`
			logKeyCol = `"key"`
		} else {
			logGroupCol = commonGroupCol
			logKeyCol = commonKeyCol
		}
	}
	// log sql type and database type
	//common.SysLog("Using Log SQL Type: " + common.LogSqlType)
}

var DB *gorm.DB

var LOG_DB *gorm.DB

func createRootAccountIfNeed() error {
	var user User
	//if user.Status != common.UserStatusEnabled {
	if err := DB.First(&user).Error; err != nil {
		common.SysLog("no user exists, create a root user for you: username is root, password is 123456")
		hashedPassword, err := common.Password2Hash("123456")
		if err != nil {
			return err
		}
		rootUser := User{
			Username:    "root",
			Password:    hashedPassword,
			Role:        common.RoleRootUser,
			Status:      common.UserStatusEnabled,
			DisplayName: "Root User",
			AccessToken: nil,
			Quota:       100000000,
		}
		DB.Create(&rootUser)
	}
	return nil
}

func CheckSetup() {
	setup := GetSetup()
	if setup == nil {
		// No setup record exists, check if we have a root user
		if RootUserExists() {
			common.SysLog("system is not initialized, but root user exists")
			// Create setup record
			newSetup := Setup{
				Version:       common.Version,
				InitializedAt: time.Now().Unix(),
			}
			err := DB.Create(&newSetup).Error
			if err != nil {
				common.SysLog("failed to create setup record: " + err.Error())
			}
			constant.Setup = true
		} else {
			common.SysLog("system is not initialized and no root user exists")
			constant.Setup = false
		}
	} else {
		// Setup record exists, system is initialized
		common.SysLog("system is already initialized at: " + time.Unix(setup.InitializedAt, 0).String())
		constant.Setup = true
	}
}

func chooseDB(envName string, isLog bool) (*gorm.DB, error) {
	defer func() {
		initCol()
	}()
	dsn := os.Getenv(envName)
	if dsn != "" {
		if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
			// Use PostgreSQL
			common.SysLog("using PostgreSQL as database")
			if !isLog {
				common.UsingPostgreSQL = true
			} else {
				common.LogSqlType = common.DatabaseTypePostgreSQL
			}
			return gorm.Open(postgres.New(postgres.Config{
				DSN:                  dsn,
				PreferSimpleProtocol: true, // disables implicit prepared statement usage
			}), &gorm.Config{
				PrepareStmt: true, // precompile SQL
			})
		}
		if strings.HasPrefix(dsn, "local") {
			common.SysLog("SQL_DSN not set, using SQLite as database")
			if !isLog {
				common.UsingSQLite = true
			} else {
				common.LogSqlType = common.DatabaseTypeSQLite
			}
			return gorm.Open(sqlite.Open(common.SQLitePath), &gorm.Config{
				PrepareStmt: true, // precompile SQL
			})
		}
		// Use MySQL
		common.SysLog("using MySQL as database")
		// check parseTime
		if !strings.Contains(dsn, "parseTime") {
			if strings.Contains(dsn, "?") {
				dsn += "&parseTime=true"
			} else {
				dsn += "?parseTime=true"
			}
		}
		if !isLog {
			common.UsingMySQL = true
		} else {
			common.LogSqlType = common.DatabaseTypeMySQL
		}
		return gorm.Open(mysql.Open(dsn), &gorm.Config{
			PrepareStmt: true, // precompile SQL
		})
	}
	// Use SQLite
	common.SysLog("SQL_DSN not set, using SQLite as database")
	common.UsingSQLite = true
	return gorm.Open(sqlite.Open(common.SQLitePath), &gorm.Config{
		PrepareStmt: true, // precompile SQL
	})
}

func InitDB() (err error) {
	db, err := chooseDB("SQL_DSN", false)
	if err == nil {
		if common.DebugEnabled {
			db = db.Debug()
		}
		DB = db
		// MySQL charset/collation startup check: ensure Chinese-capable charset
		if common.UsingMySQL {
			if err := checkMySQLChineseSupport(DB); err != nil {
				panic(err)
			}
		}
		sqlDB, err := DB.DB()
		if err != nil {
			return err
		}
		sqlDB.SetMaxIdleConns(common.GetEnvOrDefault("SQL_MAX_IDLE_CONNS", 100))
		sqlDB.SetMaxOpenConns(common.GetEnvOrDefault("SQL_MAX_OPEN_CONNS", 1000))
		sqlDB.SetConnMaxLifetime(time.Second * time.Duration(common.GetEnvOrDefault("SQL_MAX_LIFETIME", 60)))

		if !common.IsMasterNode {
			return nil
		}
		if common.UsingMySQL {
			//_, _ = sqlDB.Exec("ALTER TABLE channels MODIFY model_mapping TEXT;") // TODO: delete this line when most users have upgraded
		}
		common.SysLog("database migration started")
		err = migrateDB()
		return err
	} else {
		common.FatalLog(err)
	}
	return err
}

func InitLogDB() (err error) {
	if os.Getenv("LOG_SQL_DSN") == "" {
		LOG_DB = DB
		return
	}
	db, err := chooseDB("LOG_SQL_DSN", true)
	if err == nil {
		if common.DebugEnabled {
			db = db.Debug()
		}
		LOG_DB = db
		// If log DB is MySQL, also ensure Chinese-capable charset
		if common.LogSqlType == common.DatabaseTypeMySQL {
			if err := checkMySQLChineseSupport(LOG_DB); err != nil {
				panic(err)
			}
		}
		sqlDB, err := LOG_DB.DB()
		if err != nil {
			return err
		}
		sqlDB.SetMaxIdleConns(common.GetEnvOrDefault("SQL_MAX_IDLE_CONNS", 100))
		sqlDB.SetMaxOpenConns(common.GetEnvOrDefault("SQL_MAX_OPEN_CONNS", 1000))
		sqlDB.SetConnMaxLifetime(time.Second * time.Duration(common.GetEnvOrDefault("SQL_MAX_LIFETIME", 60)))

		if !common.IsMasterNode {
			return nil
		}
		common.SysLog("database migration started")
		err = migrateLOGDB()
		return err
	} else {
		common.FatalLog(err)
	}
	return err
}

func migrateDB() error {
	// Migrate price_amount column from float/double to decimal for existing tables
	migrateSubscriptionPlanPriceAmount()
	// Migrate model_limits column from varchar to text for existing tables
	if err := migrateTokenModelLimitsToText(); err != nil {
		return err
	}

	err := DB.AutoMigrate(
		&Channel{},
		&Token{},
		&User{},
		&PasskeyCredential{},
		&Option{},
		&Redemption{},
		&Ability{},
		&Log{},
		&Midjourney{},
		&TopUp{},
		&QuotaData{},
		&Task{},
		&Model{},
		&Vendor{},
		&PrefillGroup{},
		&Setup{},
		&TwoFA{},
		&TwoFABackupCode{},
		&Checkin{},
		&SubscriptionOrder{},
		&UserSubscription{},
		&SubscriptionPreConsumeRecord{},
		&CustomOAuthProvider{},
		&UserOAuthBinding{},
		&UserKYC{},
		&UserKYCImage{},
		&UserEnterprise{},
		&UserEnterpriseImage{},
		&BankTransferOrder{},
		&BankTransferReceipt{},
		&InvoiceRequest{},
		&InvoiceFile{},
		&SubAccountTokenBinding{},
		&FeedbackTopic{},
		&FeedbackMessage{},
		&FeedbackImage{},
		&PerfMetric{},
		&MediaStorageStats{},
		&CanvasPrompt{},
		&CanvasProject{},
		&CanvasAsset{},
		&CanvasStorageUsage{},
		&ModerationLog{},
		&ChannelModelCost{},
		&TopupPackage{},
	)
	if err != nil {
		return err
	}
	if common.UsingSQLite {
		if err := ensureSubscriptionPlanTableSQLite(); err != nil {
			return err
		}
	} else {
		if err := DB.AutoMigrate(&SubscriptionPlan{}); err != nil {
			return err
		}
	}
	if err := migrateAudioModelConfigLegacyCapability(); err != nil {
		return err
	}
	if err := migrateVideoModelConfigPipelineFlag(); err != nil {
		return err
	}
	if err := migrateAffCountBackfill(); err != nil {
		return err
	}
	if err := migratePlaygroundTabConfig(); err != nil {
		return err
	}
	if err := migrateVideoR2VACapabilityRename(); err != nil {
		return err
	}
	return nil
}

// migrateAffCountBackfill 一次性回填 aff_count。历史 bug：aff_count 自增曾被 QuotaForInviter>0 门控，
// 未配置邀请奖励时邀请人数恒为 0（邀请关系 inviter_id 仍正确写入）。按 inviter_id 重算真实计数写回。
// 一次性守卫：marker 存在即已回填，绝不再跑。三库通用（GORM 抽象，软删用户自动排除）。
func migrateAffCountBackfill() error {
	const markerKey = "aff_count_backfilled"
	var marker Option
	if err := DB.Where(Option{Key: markerKey}).First(&marker).Error; err == nil {
		return nil // 已回填
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		// 先清零历史残留的非零计数，保证「重算」语义完整：
		// 曾配过邀请奖励、被邀请人后来被删除的旧 inviter 不在下方分组结果里，不清零会永久残留脏值。
		// 仅触及非零行（现网计数普遍为 0，基本 no-op）。
		if err := tx.Model(&User{}).Where("aff_count > ?", 0).Update("aff_count", 0).Error; err != nil {
			return err
		}
		type grp struct {
			InviterId int
			Cnt       int
		}
		var groups []grp
		if err := tx.Model(&User{}).
			Select("inviter_id, COUNT(*) as cnt").
			Where("inviter_id != ?", 0).
			Group("inviter_id").
			Scan(&groups).Error; err != nil {
			return err
		}
		for _, g := range groups {
			if err := tx.Model(&User{}).Where("id = ?", g.InviterId).
				Update("aff_count", g.Cnt).Error; err != nil {
				return err
			}
		}
		common.SysLog(fmt.Sprintf("Backfilled aff_count for %d inviters", len(groups)))
		// 落一次性标记（无论有无数据都标，保证只在首次运行回填）。
		return tx.Create(&Option{Key: markerKey, Value: "true"}).Error
	})
}

// migrateAudioModelConfigLegacyCapability 语音大类拆细的兼容迁移。拆细前音频能力标签只有
// 「语音合成」(IndexTTS 系,体验区提交 voice 字段);拆细后「语音合成」被复用为合并的 Omni 语音
// tab(提交 ref_audio/speaker),IndexTTS 归新标签「情感合成」。旧 AudioModelConfig 里标了
// 「语音合成」的模型若不改标,会落进 Omni tab、发 ref_audio 而非 voice → 后端 materializeTTSInputs
// 报错。故把 capabilities 里的「语音合成」整体改成「情感合成」(拆细前该大类只有 IndexTTS 系,
// 全量改安全)。幂等:改后不含旧标签即已迁移;无该配置或解析失败则跳过,绝不破坏原值。
// 直接写 Option 行(migrateDB 早于 InitOptionMap,OptionMap 稍后加载即拿到迁移后的值)。
func migrateAudioModelConfigLegacyCapability() error {
	const markerKey = "audio_capability_split_migrated"
	// 一次性守卫:标记存在即已迁移过,绝不再跑。否则升级后运营给新 Omni 模型正确标的
	// 新语义「语音合成」(合并 tab)会在下次重启被改成「情感合成」→ 腐蚀有效配置。
	var marker Option
	if err := DB.Where(Option{Key: markerKey}).First(&marker).Error; err == nil {
		return nil // 已迁移
	}
	// 配置改写 + 写标记放同一事务:崩溃不会留下"已改未标"(下次重跑误伤新标签)或
	// "已标未改"(旧标签永不迁移)的半状态。
	return DB.Transaction(func(tx *gorm.DB) error {
		var option Option
		if err := tx.Where(Option{Key: "AudioModelConfig"}).First(&option).Error; err == nil {
			raw := strings.TrimSpace(option.Value)
			if raw != "" && strings.Contains(raw, "语音合成") {
				var cfg map[string]interface{}
				if common.UnmarshalJsonStr(raw, &cfg) == nil {
					changed := false
					fixCaps := func(node interface{}) {
						m, ok := node.(map[string]interface{})
						if !ok {
							return
						}
						caps, ok := m["capabilities"].([]interface{})
						if !ok {
							return
						}
						for i, c := range caps {
							if s, ok := c.(string); ok && s == "语音合成" {
								caps[i] = "情感合成"
								changed = true
							}
						}
					}
					fixCaps(cfg["default"])
					if models, ok := cfg["models"].(map[string]interface{}); ok {
						for _, v := range models {
							fixCaps(v)
						}
					}
					if changed {
						out, err := common.Marshal(cfg)
						if err != nil {
							return err
						}
						option.Value = string(out)
						if err := tx.Save(&option).Error; err != nil {
							return err
						}
						common.SysLog("Migrated AudioModelConfig capability 语音合成 -> 情感合成 (audio tab split)")
					}
				}
			}
		}
		// 落一次性标记(无论有没有旧数据都标,保证只在首次运行迁移)。
		return tx.Create(&Option{Key: markerKey, Value: "true"}).Error
	})
}

// migrateVideoModelConfigPipelineFlag 给视频模型补 pipeline 标记。体验区的 1080P 自动超分、
// 自动配音、插帧(metadata.target_fps)都是自建 gpustackplus 引擎特有的玩法,原来的判据里没有
// 任何一项识别渠道:只要某个第三方模型(Sora/MiniMax 等)恰好配了 480P 与 1080P 两个档位,用户
// 选 1080P 就会被降级成 480P 再丢给自建超分模型。改判后前端只认显式的 models[x].pipeline,
// 未标记即原样透传。本迁移把存量的自建模型补上 pipeline:true,保证升级前后行为一致,
// 不留「部署完到运营手动勾选」的窗口期。
//
// 判据是渠道归属(该模型挂在 type=GPUStackPlus 的渠道上),不是尺寸档位:sizes 描述的是
// 「支持哪些输出档位」,与渠道归属无关,拿它推断两头都会错——第三方模型恰好配了 480+1080
// 会被误标(假阳性),而自建的 i2v/关键帧/数字人/视频编辑模型根本不配 sizes
// (followsInput,输出跟随输入)、只配 720P 的自建 t2v 也一样,会被漏标(假阴性)。
// 漏标的后果是真回归:这些模型升级后插帧开关消失、target_fps 停发、配音段不再接。
//
// 幂等:一次性标记守卫,标记存在即跳过;否则运营事后取消勾选会在下次重启被覆盖回来。
// 无该配置或解析失败则只落标记、绝不破坏原值。直接写 Option 行(migrateDB 早于 InitOptionMap,
// 且 Channel/Ability 的 AutoMigrate 在本函数之前,建表与存量数据都已就位)。
func migrateVideoModelConfigPipelineFlag() error {
	const markerKey = "video_pipeline_flag_migrated"
	var marker Option
	if err := DB.Where(Option{Key: markerKey}).First(&marker).Error; err == nil {
		return nil // 已迁移
	}
	selfHosted, err := selfHostedVideoModelNames()
	if err != nil {
		return err
	}
	// 配置改写 + 写标记放同一事务:崩溃不会留下"已改未标"或"已标未改"的半状态。
	return DB.Transaction(func(tx *gorm.DB) error {
		var option Option
		if err := tx.Where(Option{Key: "VideoModelConfig"}).First(&option).Error; err == nil {
			raw := strings.TrimSpace(option.Value)
			if raw != "" {
				var cfg map[string]interface{}
				if common.UnmarshalJsonStr(raw, &cfg) == nil {
					changed := false
					if models, ok := cfg["models"].(map[string]interface{}); ok {
						for name, v := range models {
							m, ok := v.(map[string]interface{})
							if !ok {
								continue
							}
							if _, exists := m["pipeline"]; exists {
								continue // 已有显式取值,不覆盖
							}
							if !selfHosted[name] {
								continue
							}
							m["pipeline"] = true
							changed = true
							common.SysLog(fmt.Sprintf("VideoModelConfig: marked %s as self-hosted pipeline model", name))
						}
					}
					if changed {
						out, err := common.Marshal(cfg)
						if err != nil {
							return err
						}
						option.Value = string(out)
						if err := tx.Save(&option).Error; err != nil {
							return err
						}
					}
				}
			}
		}
		// 落一次性标记(无论有没有可迁移的数据都标,保证只在首次运行迁移)。
		return tx.Create(&Option{Key: markerKey, Value: "true"}).Error
	})
}

// selfHostedVideoModelNames 取出所有挂在自建 gpustackplus 渠道上的对外模型名。
// abilities 存的就是对外模型名,与「视频模型配置」里填的是同一套(配置页已注明填对外名),
// 渠道做了重定向也不影响。join 写法仿 GetAllEnableAbilityWithChannels(model/ability.go),
// 三种数据库通用。
// 不按 enabled 过滤:渠道被临时禁用不代表它不是自建模型,过滤掉会让那批模型漏标、
// 升级后丢掉插帧与配音能力。
func selfHostedVideoModelNames() (map[string]bool, error) {
	var rows []struct{ Model string }
	err := DB.Table("abilities").
		Select("DISTINCT abilities.model AS model").
		Joins("left join channels on abilities.channel_id = channels.id").
		Where("channels.type = ?", constant.ChannelTypeGPUStackPlus).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(rows))
	for _, r := range rows {
		names[r.Model] = true
	}
	return names, nil
}

// migrateVideoR2VACapabilityRename 把存量 VideoModelConfig 里的能力标签
// 「参考生视频」改写成它的正名「视频编辑」。
//
// 「参考生视频」曾经是「视频编辑」的旧名(2026-07 那轮能力标签重命名前的叫法)。
// 2026-08 新增了正式的「参考生视频」tab(MiniMax H3 Ref2VA),这个词从此有了自己的
// 含义,旧标签必须清掉——否则留着它的模型会**同时踩两个坑**:
//
//	体验区的 videoModelSet 判据是 caps.includes(pageCapability),于是
//	  · 「视频编辑」tab —— 旧标签不再命中(前端那条 legacy alias 已随本轮摘除),模型消失
//	  · 「参考生视频」tab —— 旧标签**直接命中新 tab 的 capability**,模型错误地出现在这里
//
// 后者才是真正危险的:一个 Bernini 视频编辑模型跑到参考生视频 tab 里,用户提交就是
// 拿 task_type=r2va 打给一个不认识它的模型。
//
// **不能指望 migratePlaygroundTabConfig 顺手规整**:它的 vace 规格虽然认
// aliases:["参考生视频"],但只用来决定往哪个 tab 扇出,全程只写 m["tabs"]、
// 从不改 m["capabilities"](见 migrate_playground_tabs.go),而体验区读的正是
// capabilities。它自己还有一次性 marker,已迁移过的部署不会再跑第二遍。
//
// 改写方向是安全的:本轮之前这个字段里的「参考生视频」只可能是视频编辑的旧名——
// 新 tab 本轮才出现,而本迁移在升级时就跑完,那时还没有人能配出真正的 r2va 模型。
// 与 migratePlaygroundTabConfig 的先后无所谓:先扇出则按 alias 命中 vace,先改名则
// 按正名命中 vace,两种顺序结果相同。
//
// 幂等:一次性标记守卫。无该配置或解析失败则只落标记、绝不破坏原值。
func migrateVideoR2VACapabilityRename() error {
	const markerKey = "video_r2va_capability_renamed"
	const legacyTag = "参考生视频" // 本轮起改指新 tab,故存量值必须改名
	const canonicalTag = "视频编辑"
	var marker Option
	if err := DB.Where(Option{Key: markerKey}).First(&marker).Error; err == nil {
		return nil // 已迁移
	}
	// 配置改写 + 写标记放同一事务:崩溃不会留下"已改未标"或"已标未改"的半状态。
	return DB.Transaction(func(tx *gorm.DB) error {
		var option Option
		if err := tx.Where(Option{Key: "VideoModelConfig"}).First(&option).Error; err == nil {
			raw := strings.TrimSpace(option.Value)
			if raw != "" {
				var cfg map[string]interface{}
				if common.UnmarshalJsonStr(raw, &cfg) == nil {
					changed := false
					if models, ok := cfg["models"].(map[string]interface{}); ok {
						for name, v := range models {
							m, ok := v.(map[string]interface{})
							if !ok {
								continue // 旧形态(值曾是尺寸数组):没有 capabilities,无可改
							}
							caps, ok := m["capabilities"].([]interface{})
							if !ok {
								continue
							}
							// 先看有没有正名:两个都在时旧标签直接删,不能改成重复项。
							hasCanonical := false
							for _, c := range caps {
								if s, _ := c.(string); strings.TrimSpace(s) == canonicalTag {
									hasCanonical = true
									break
								}
							}
							out := make([]interface{}, 0, len(caps))
							hit := false
							for _, c := range caps {
								s, _ := c.(string)
								if strings.TrimSpace(s) != legacyTag {
									out = append(out, c)
									continue
								}
								hit = true
								if !hasCanonical {
									out = append(out, canonicalTag)
									hasCanonical = true
								}
							}
							if !hit {
								continue
							}
							m["capabilities"] = out
							changed = true
							common.SysLog(fmt.Sprintf("VideoModelConfig: renamed legacy capability %q to %q on %s", legacyTag, canonicalTag, name))
						}
					}
					if changed {
						out, err := common.Marshal(cfg)
						if err != nil {
							return err
						}
						option.Value = string(out)
						if err := tx.Save(&option).Error; err != nil {
							return err
						}
					}
				}
			}
		}
		// 落一次性标记(无论有没有可迁移的数据都标,保证只在首次运行迁移)。
		return tx.Create(&Option{Key: markerKey, Value: "true"}).Error
	})
}

func migrateDBFast() error {

	var wg sync.WaitGroup

	migrations := []struct {
		model interface{}
		name  string
	}{
		{&Channel{}, "Channel"},
		{&Token{}, "Token"},
		{&User{}, "User"},
		{&PasskeyCredential{}, "PasskeyCredential"},
		{&Option{}, "Option"},
		{&Redemption{}, "Redemption"},
		{&Ability{}, "Ability"},
		{&Log{}, "Log"},
		{&Midjourney{}, "Midjourney"},
		{&TopUp{}, "TopUp"},
		{&QuotaData{}, "QuotaData"},
		{&Task{}, "Task"},
		{&Model{}, "Model"},
		{&Vendor{}, "Vendor"},
		{&PrefillGroup{}, "PrefillGroup"},
		{&Setup{}, "Setup"},
		{&TwoFA{}, "TwoFA"},
		{&TwoFABackupCode{}, "TwoFABackupCode"},
		{&Checkin{}, "Checkin"},
		{&SubscriptionOrder{}, "SubscriptionOrder"},
		{&UserSubscription{}, "UserSubscription"},
		{&SubscriptionPreConsumeRecord{}, "SubscriptionPreConsumeRecord"},
		{&CustomOAuthProvider{}, "CustomOAuthProvider"},
		{&UserOAuthBinding{}, "UserOAuthBinding"},
		{&UserKYC{}, "UserKYC"},
		{&UserKYCImage{}, "UserKYCImage"},
		{&UserEnterprise{}, "UserEnterprise"},
		{&UserEnterpriseImage{}, "UserEnterpriseImage"},
		{&BankTransferOrder{}, "BankTransferOrder"},
		{&BankTransferReceipt{}, "BankTransferReceipt"},
		{&InvoiceRequest{}, "InvoiceRequest"},
		{&InvoiceFile{}, "InvoiceFile"},
		{&SubAccountTokenBinding{}, "SubAccountTokenBinding"},
		{&FeedbackTopic{}, "FeedbackTopic"},
		{&FeedbackMessage{}, "FeedbackMessage"},
		{&FeedbackImage{}, "FeedbackImage"},
		{&PerfMetric{}, "PerfMetric"},
		{&ModerationLog{}, "ModerationLog"},
	}
	// 动态计算migration数量，确保errChan缓冲区足够大
	errChan := make(chan error, len(migrations))

	for _, m := range migrations {
		wg.Add(1)
		go func(model interface{}, name string) {
			defer wg.Done()
			if err := DB.AutoMigrate(model); err != nil {
				errChan <- fmt.Errorf("failed to migrate %s: %v", name, err)
			}
		}(m.model, m.name)
	}

	// Wait for all migrations to complete
	wg.Wait()
	close(errChan)

	// Check for any errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}
	if common.UsingSQLite {
		if err := ensureSubscriptionPlanTableSQLite(); err != nil {
			return err
		}
	} else {
		if err := DB.AutoMigrate(&SubscriptionPlan{}); err != nil {
			return err
		}
	}
	common.SysLog("database migrated")
	return nil
}

func migrateLOGDB() error {
	var err error
	if err = LOG_DB.AutoMigrate(&Log{}); err != nil {
		return err
	}
	return nil
}

type sqliteColumnDef struct {
	Name string
	DDL  string
}

func ensureSubscriptionPlanTableSQLite() error {
	if !common.UsingSQLite {
		return nil
	}
	tableName := "subscription_plans"
	if !DB.Migrator().HasTable(tableName) {
		createSQL := `CREATE TABLE ` + "`" + tableName + "`" + ` (
` + "`id`" + ` integer,
` + "`title`" + ` varchar(128) NOT NULL,
` + "`subtitle`" + ` varchar(255) DEFAULT '',
` + "`price_amount`" + ` decimal(10,6) NOT NULL,
` + "`currency`" + ` varchar(8) NOT NULL DEFAULT 'USD',
` + "`duration_unit`" + ` varchar(16) NOT NULL DEFAULT 'month',
` + "`duration_value`" + ` integer NOT NULL DEFAULT 1,
` + "`custom_seconds`" + ` bigint NOT NULL DEFAULT 0,
` + "`enabled`" + ` numeric DEFAULT 1,
` + "`sort_order`" + ` integer DEFAULT 0,
` + "`stripe_price_id`" + ` varchar(128) DEFAULT '',
` + "`creem_product_id`" + ` varchar(128) DEFAULT '',
` + "`max_purchase_per_user`" + ` integer DEFAULT 0,
` + "`upgrade_group`" + ` varchar(64) DEFAULT '',
` + "`total_amount`" + ` bigint NOT NULL DEFAULT 0,
` + "`quota_reset_period`" + ` varchar(16) DEFAULT 'never',
` + "`quota_reset_custom_seconds`" + ` bigint DEFAULT 0,
` + "`created_at`" + ` bigint,
` + "`updated_at`" + ` bigint,
PRIMARY KEY (` + "`id`" + `)
)`
		return DB.Exec(createSQL).Error
	}
	var cols []struct {
		Name string `gorm:"column:name"`
	}
	if err := DB.Raw("PRAGMA table_info(`" + tableName + "`)").Scan(&cols).Error; err != nil {
		return err
	}
	existing := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		existing[c.Name] = struct{}{}
	}
	required := []sqliteColumnDef{
		{Name: "title", DDL: "`title` varchar(128) NOT NULL"},
		{Name: "subtitle", DDL: "`subtitle` varchar(255) DEFAULT ''"},
		{Name: "price_amount", DDL: "`price_amount` decimal(10,6) NOT NULL"},
		{Name: "currency", DDL: "`currency` varchar(8) NOT NULL DEFAULT 'USD'"},
		{Name: "duration_unit", DDL: "`duration_unit` varchar(16) NOT NULL DEFAULT 'month'"},
		{Name: "duration_value", DDL: "`duration_value` integer NOT NULL DEFAULT 1"},
		{Name: "custom_seconds", DDL: "`custom_seconds` bigint NOT NULL DEFAULT 0"},
		{Name: "enabled", DDL: "`enabled` numeric DEFAULT 1"},
		{Name: "sort_order", DDL: "`sort_order` integer DEFAULT 0"},
		{Name: "stripe_price_id", DDL: "`stripe_price_id` varchar(128) DEFAULT ''"},
		{Name: "creem_product_id", DDL: "`creem_product_id` varchar(128) DEFAULT ''"},
		{Name: "max_purchase_per_user", DDL: "`max_purchase_per_user` integer DEFAULT 0"},
		{Name: "upgrade_group", DDL: "`upgrade_group` varchar(64) DEFAULT ''"},
		{Name: "total_amount", DDL: "`total_amount` bigint NOT NULL DEFAULT 0"},
		{Name: "quota_reset_period", DDL: "`quota_reset_period` varchar(16) DEFAULT 'never'"},
		{Name: "quota_reset_custom_seconds", DDL: "`quota_reset_custom_seconds` bigint DEFAULT 0"},
		{Name: "created_at", DDL: "`created_at` bigint"},
		{Name: "updated_at", DDL: "`updated_at` bigint"},
	}
	for _, col := range required {
		if _, ok := existing[col.Name]; ok {
			continue
		}
		if err := DB.Exec("ALTER TABLE `" + tableName + "` ADD COLUMN " + col.DDL).Error; err != nil {
			return err
		}
	}
	return nil
}

// migrateTokenModelLimitsToText migrates model_limits column from varchar(1024) to text
// This is safe to run multiple times - it checks the column type first
func migrateTokenModelLimitsToText() error {
	// SQLite uses type affinity, so TEXT and VARCHAR are effectively the same — no migration needed
	if common.UsingSQLite {
		return nil
	}

	tableName := "tokens"
	columnName := "model_limits"

	if !DB.Migrator().HasTable(tableName) {
		return nil
	}

	if !DB.Migrator().HasColumn(&Token{}, columnName) {
		return nil
	}

	var alterSQL string
	if common.UsingPostgreSQL {
		var dataType string
		if err := DB.Raw(`SELECT data_type FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`,
			tableName, columnName).Scan(&dataType).Error; err != nil {
			common.SysLog(fmt.Sprintf("Warning: failed to query metadata for %s.%s: %v", tableName, columnName, err))
		} else if dataType == "text" {
			return nil
		}
		alterSQL = fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s TYPE text`, tableName, columnName)
	} else if common.UsingMySQL {
		var columnType string
		if err := DB.Raw(`SELECT COLUMN_TYPE FROM information_schema.columns
				WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`,
			tableName, columnName).Scan(&columnType).Error; err != nil {
			common.SysLog(fmt.Sprintf("Warning: failed to query metadata for %s.%s: %v", tableName, columnName, err))
		} else if strings.ToLower(columnType) == "text" {
			return nil
		}
		alterSQL = fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s text", tableName, columnName)
	} else {
		return nil
	}

	if alterSQL != "" {
		if err := DB.Exec(alterSQL).Error; err != nil {
			return fmt.Errorf("failed to migrate %s.%s to text: %w", tableName, columnName, err)
		}
		common.SysLog(fmt.Sprintf("Successfully migrated %s.%s to text", tableName, columnName))
	}
	return nil
}

// migrateSubscriptionPlanPriceAmount migrates price_amount column from float/double to decimal(10,6)
// This is safe to run multiple times - it checks the column type first
func migrateSubscriptionPlanPriceAmount() {
	// SQLite doesn't support ALTER COLUMN, and its type affinity handles this automatically
	// Skip early to avoid GORM parsing the existing table DDL which may cause issues
	if common.UsingSQLite {
		return
	}

	tableName := "subscription_plans"
	columnName := "price_amount"

	// Check if table exists first
	if !DB.Migrator().HasTable(tableName) {
		return
	}

	// Check if column exists
	if !DB.Migrator().HasColumn(&SubscriptionPlan{}, columnName) {
		return
	}

	var alterSQL string
	if common.UsingPostgreSQL {
		// PostgreSQL: Check if already decimal/numeric
		var dataType string
		if err := DB.Raw(`SELECT data_type FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`,
			tableName, columnName).Scan(&dataType).Error; err != nil {
			common.SysLog(fmt.Sprintf("Warning: failed to query metadata for %s.%s: %v", tableName, columnName, err))
		} else if dataType == "numeric" {
			return // Already decimal/numeric
		}
		alterSQL = fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s TYPE decimal(10,6) USING %s::decimal(10,6)`,
			tableName, columnName, columnName)
	} else if common.UsingMySQL {
		// MySQL: Check if already decimal
		var columnType string
		if err := DB.Raw(`SELECT COLUMN_TYPE FROM information_schema.columns
				WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`,
			tableName, columnName).Scan(&columnType).Error; err != nil {
			common.SysLog(fmt.Sprintf("Warning: failed to query metadata for %s.%s: %v", tableName, columnName, err))
		} else if strings.HasPrefix(strings.ToLower(columnType), "decimal") {
			return // Already decimal
		}
		alterSQL = fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s decimal(10,6) NOT NULL DEFAULT 0",
			tableName, columnName)
	} else {
		return
	}

	if alterSQL != "" {
		if err := DB.Exec(alterSQL).Error; err != nil {
			common.SysLog(fmt.Sprintf("Warning: failed to migrate %s.%s to decimal: %v", tableName, columnName, err))
		} else {
			common.SysLog(fmt.Sprintf("Successfully migrated %s.%s to decimal(10,6)", tableName, columnName))
		}
	}
}

func closeDB(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	err = sqlDB.Close()
	return err
}

func CloseDB() error {
	if LOG_DB != DB {
		err := closeDB(LOG_DB)
		if err != nil {
			return err
		}
	}
	return closeDB(DB)
}

// checkMySQLChineseSupport ensures the MySQL connection and current schema
// default charset/collation can store Chinese characters. It allows common
// Chinese-capable charsets (utf8mb4, utf8, gbk, big5, gb18030) and panics otherwise.
func checkMySQLChineseSupport(db *gorm.DB) error {
	// 仅检测：当前库默认字符集/排序规则 + 各表的排序规则（隐含字符集）

	// Read current schema defaults
	var schemaCharset, schemaCollation string
	err := db.Raw("SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = DATABASE()").Row().Scan(&schemaCharset, &schemaCollation)
	if err != nil {
		return fmt.Errorf("读取当前库默认字符集/排序规则失败 / Failed to read schema default charset/collation: %v", err)
	}

	toLower := func(s string) string { return strings.ToLower(s) }
	// Allowed charsets that can store Chinese text
	allowedCharsets := map[string]string{
		"utf8mb4": "utf8mb4_",
		"utf8":    "utf8_",
		"gbk":     "gbk_",
		"big5":    "big5_",
		"gb18030": "gb18030_",
	}
	isChineseCapable := func(cs, cl string) bool {
		csLower := toLower(cs)
		clLower := toLower(cl)
		if prefix, ok := allowedCharsets[csLower]; ok {
			if clLower == "" {
				return true
			}
			return strings.HasPrefix(clLower, prefix)
		}
		// 如果仅提供了排序规则，尝试按排序规则前缀判断
		for _, prefix := range allowedCharsets {
			if strings.HasPrefix(clLower, prefix) {
				return true
			}
		}
		return false
	}

	// 1) 当前库默认值必须支持中文
	if !isChineseCapable(schemaCharset, schemaCollation) {
		return fmt.Errorf("当前库默认字符集/排序规则不支持中文：schema(%s/%s)。请将库设置为 utf8mb4/utf8/gbk/big5/gb18030 / Schema default charset/collation is not Chinese-capable: schema(%s/%s). Please set to utf8mb4/utf8/gbk/big5/gb18030",
			schemaCharset, schemaCollation, schemaCharset, schemaCollation)
	}

	// 2) 所有物理表的排序规则（隐含字符集）必须支持中文
	type tableInfo struct {
		Name      string
		Collation *string
	}
	var tables []tableInfo
	if err := db.Raw("SELECT TABLE_NAME, TABLE_COLLATION FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'").Scan(&tables).Error; err != nil {
		return fmt.Errorf("读取表排序规则失败 / Failed to read table collations: %v", err)
	}

	var badTables []string
	for _, t := range tables {
		// NULL 或空表示继承库默认设置，已在上面校验库默认，视为通过
		if t.Collation == nil || *t.Collation == "" {
			continue
		}
		cl := *t.Collation
		// 仅凭排序规则判断是否中文可用
		ok := false
		lower := strings.ToLower(cl)
		for _, prefix := range allowedCharsets {
			if strings.HasPrefix(lower, prefix) {
				ok = true
				break
			}
		}
		if !ok {
			badTables = append(badTables, fmt.Sprintf("%s(%s)", t.Name, cl))
		}
	}

	if len(badTables) > 0 {
		// 限制输出数量以避免日志过长
		maxShow := 20
		shown := badTables
		if len(shown) > maxShow {
			shown = shown[:maxShow]
		}
		return fmt.Errorf(
			"存在不支持中文的表，请修复其排序规则/字符集。示例（最多展示 %d 项）：%v / Found tables not Chinese-capable. Please fix their collation/charset. Examples (showing up to %d): %v",
			maxShow, shown, maxShow, shown,
		)
	}
	return nil
}

var (
	lastPingTime time.Time
	pingMutex    sync.Mutex
)

func PingDB() error {
	pingMutex.Lock()
	defer pingMutex.Unlock()

	if time.Since(lastPingTime) < time.Second*10 {
		return nil
	}

	sqlDB, err := DB.DB()
	if err != nil {
		log.Printf("Error getting sql.DB from GORM: %v", err)
		return err
	}

	err = sqlDB.Ping()
	if err != nil {
		log.Printf("Error pinging DB: %v", err)
		return err
	}

	lastPingTime = time.Now()
	common.SysLog("Database pinged successfully")
	return nil
}
