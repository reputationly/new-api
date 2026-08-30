package model

import (
	"fmt"
	"regexp"

	"github.com/QuantumNous/new-api/common"
)

// commaTypeRe 匹配「类型名(数字,数字)」形式的列类型声明，如 decimal(10,6) / numeric(12,2)。
//
// 刻意只认括号内是「数字,数字」的写法，避免误伤：
//   - varchar(255)      —— 括号内无逗号，不匹配；
//   - PRIMARY KEY (`id`) —— 括号内非数字，不匹配；
//   - 复合索引 (`a`,`b`) —— 同上。
var commaTypeRe = regexp.MustCompile(`([A-Za-z]+)\(\s*\d+\s*,\s*\d+\s*\)`)

// repairSQLiteCommaColumnTypes 修复历史遗留的「带逗号列类型」SQLite schema。
//
// 背景：glebarez/sqlite 的 DDL 解析器抓列类型的正则字符集不含逗号，会把 decimal(10,6)
// 读成 decimal(10，于是每次 AutoMigrate 都误判该列需要变更、走 recreateTable，并在参数
// 替换时把类型写坏成 ?,6)，最终报 "invalid DDL, unbalanced brackets" 让 InitDB 直接 FATAL。
// 症状是「第一次启动正常、第二次启动起不来」。
//
// 模型侧已改用 precision/scale 让新库不再产生这种类型（见 subscription.go 等），
// 但**旧版本建出来的库仍然坏着**，而且坏到起不来、连迁移都跑不到。这个函数在
// AutoMigrate 之前把这类声明规范化掉，让存量库能自愈，而不是要求用户删库重来。
//
// 去掉精度对 SQLite 无损：它是类型亲和性（type affinity），decimal 与 decimal(10,6)
// 同属 NUMERIC 亲和性，存取行为一致。规范化成无逗号形式后，后续 AutoMigrate 想把它
// 再调整成模型声明的类型也能正常完成——那条路径不再解析到破损 DDL。
//
// 仅 SQLite 执行；MySQL / PostgreSQL 直接返回。
func repairSQLiteCommaColumnTypes() error {
	if !common.UsingSQLite {
		return nil
	}

	type tableDDL struct {
		Name string `gorm:"column:name"`
		SQL  string `gorm:"column:sql"`
	}
	var rows []tableDDL
	// 先粗筛出「括号里带逗号」的表，再在 Go 侧用正则精确判断。
	if err := DB.Raw(`SELECT name, sql FROM sqlite_master
		WHERE type = 'table' AND sql IS NOT NULL AND sql LIKE '%,%)%'`).Scan(&rows).Error; err != nil {
		return err
	}

	repaired := make([]string, 0, 4)
	for _, r := range rows {
		fixed := commaTypeRe.ReplaceAllString(r.SQL, "$1")
		if fixed == r.SQL {
			continue
		}
		repaired = append(repaired, r.Name)
		// writable_schema 是唯一能改列类型声明而不重建表的手段。风险控制：
		// 只替换类型声明本身（正则已限定形状），不动表名/列名/约束，改完立刻做完整性校验。
		if err := DB.Exec("PRAGMA writable_schema = ON").Error; err != nil {
			return err
		}
		err := DB.Exec("UPDATE sqlite_master SET sql = ? WHERE type = 'table' AND name = ?", fixed, r.Name).Error
		if offErr := DB.Exec("PRAGMA writable_schema = OFF").Error; offErr != nil && err == nil {
			err = offErr
		}
		if err != nil {
			return fmt.Errorf("repair sqlite column type for %s: %w", r.Name, err)
		}
	}

	if len(repaired) == 0 {
		return nil
	}

	var check string
	if err := DB.Raw("PRAGMA integrity_check").Scan(&check).Error; err != nil {
		return fmt.Errorf("integrity check after schema repair: %w", err)
	}
	if check != "ok" {
		return fmt.Errorf("sqlite integrity check failed after schema repair: %s", check)
	}

	common.SysLog(fmt.Sprintf(
		"SQLite schema 修复：已规范化 %v 的带逗号列类型（如 decimal(10,6) → decimal）。"+
			"这类声明会让驱动的 DDL 解析器出错，导致第二次启动 FATAL。", repaired))
	return nil
}
