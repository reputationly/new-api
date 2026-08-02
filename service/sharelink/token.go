// Package sharelink 为生成结果签发免登录分享 token。
//
// 微信内置浏览器不给 <video>/<audio> 长按保存菜单、忽略 a[download]、禁用
// navigator.share，成品在微信里带不走。唯一出路是把用户引到外部浏览器，但
// /pg/videos/:task_id/content 挂着 UserAuth——跳出去是个没登录的干净浏览器，
// 看到的是登录页而不是刚生成的视频。本包提供的 token 就是这条路的钥匙。
//
// 无状态设计：token 自带 taskID 与过期时间并用 HMAC 签名，不建表、不入库。
// 代价是签发后无法单独撤销，由 TTL 兜底。
package sharelink

import (
	"crypto/hmac"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// TTL 与 OBS 对象生命周期、签名 URL TTL 对齐（均为 7 天，见
// setting/system_setting/media_storage.go）。链接失效那天对象本身也正好被 OBS
// 生命周期清掉，不会出现「链接还能打开但内容 404」的错位。
const TTL = 7 * 24 * time.Hour

var (
	ErrMalformed = errors.New("share token malformed")
	ErrSignature = errors.New("share token signature mismatch")
	ErrExpired   = errors.New("share token expired")
)

// Sign 为某个用户的某个任务签发 token，返回 token 与过期时间戳（Unix 秒）。
//
// payload 里带 userID 而不只是 taskID，是为了让解析端能按 (user_id, task_id) 查库。
// tasks.task_id 上只有普通索引、没有唯一约束——今天它恰好是本地 crypto/rand 生成的
// 32 位随机串（model.GenerateTaskID），撞不了；但把「不会越权」建立在这个未被数据库
// 强制的前提上太脆。带上 userID 后，即便 task_id 真撞了，也只会命中签发者自己的任务。
//
// 签名对象是 base64 编码后的 payload 而非原始串：task_id 是 varchar(191)，内容不可控，
// 先编码再签能避免分隔符出现在签名输入里造成歧义。
func Sign(userID int, taskID string, now time.Time) (string, int64) {
	exp := now.Add(TTL).Unix()
	payload := base64.RawURLEncoding.EncodeToString(
		[]byte(strconv.Itoa(userID) + "." + taskID + "." + strconv.FormatInt(exp, 10)),
	)
	return payload + "." + common.GenerateHMAC(payload), exp
}

// Verify 校验 token 并取回 userID 与 taskID。
//
// 必须先验签名再验过期：顺序反了的话，攻击者能从「已过期」和「签名错」两种不同
// 响应里反推出 payload 的合法结构，等于给爆破提供了预言机。
func Verify(token string, now time.Time) (int, string, error) {
	sep := strings.LastIndex(token, ".")
	if sep <= 0 || sep == len(token)-1 {
		return 0, "", ErrMalformed
	}
	payload, sig := token[:sep], token[sep+1:]

	if !hmac.Equal([]byte(sig), []byte(common.GenerateHMAC(payload))) {
		return 0, "", ErrSignature
	}

	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return 0, "", ErrMalformed
	}
	// 布局：<userID>.<taskID>.<exp>。userID 是纯数字、exp 是纯数字，都不含 '.'，
	// 而 task_id 自身可能含 '.'，所以取首尾两个分隔符、中间整段都算 taskID。
	decoded := string(raw)
	head := strings.Index(decoded, ".")
	tail := strings.LastIndex(decoded, ".")
	if head <= 0 || tail <= head {
		return 0, "", ErrMalformed
	}
	userID, err := strconv.Atoi(decoded[:head])
	if err != nil {
		return 0, "", ErrMalformed
	}
	exp, err := strconv.ParseInt(decoded[tail+1:], 10, 64)
	if err != nil {
		return 0, "", ErrMalformed
	}
	if now.Unix() > exp {
		return 0, "", ErrExpired
	}
	return userID, decoded[head+1 : tail], nil
}
