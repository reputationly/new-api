// 移动云 MoMA 素材资产库客户端。
//
// 与方舟素材库（Action 协议 + 火山 V4 签名）是两套互不兼容的接口，做的事却一样：把参考图 /
// 参考视频入库拿到 asset://<id>，再用它调视频生成，避开 Seedance 对直传人脸素材的输入审核。
//
// 这里只实现 Uploader 依赖的 4 个方法，并发、去重、轮询 Active、ID 缓存全部沿用 Uploader。
//
// 接口文档：https://ecloud.10086.cn/op-help-center/doc/article/100301
// 签名规则：https://ecloud.10086.cn/op-help-center/doc/article/24283
package arkasset

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	// EcloudDefaultEndpoint 移动云素材资产库的固定入口，与视频生成网关（*.cmecloud.cn）不同域。
	EcloudDefaultEndpoint = "https://ecloud.10086.cn"

	ecloudAPIPrefix       = "/api/openapi-maas/exp/aicc/v2"
	ecloudAssetGroupPath  = ecloudAPIPrefix + "/asset-group"
	ecloudAssetPath       = ecloudAPIPrefix + "/asset"
	ecloudSigningPrefix   = "BC_SIGNATURE&"
	ecloudTimestampFormat = "2006-01-02T15:04:05Z"
)

// ecloudZone 移动云签名要的是「本地时间 + 字面 Z」：官方 Java / Python / Postman 示例都是直接
// 拿本机时间拼 Z，发 UTC 会被判 Invalid parameter Timestamp（实测 UTC 07:24:34Z 报错、北京时间
// 15:24:34Z 通过时间校验）。这里固定北京时间，不用 time.LoadLocation —— 精简镜像里常常没有 tzdata。
var ecloudZone = time.FixedZone("CST", 8*60*60)

type EcloudConfig struct {
	Endpoint  string // 空 = EcloudDefaultEndpoint
	AccessKey string // 移动云 AccessKey，必填（移动云统一签名）
	SecretKey string // 移动云 SecretKey，必填
}

func (c EcloudConfig) endpoint() string {
	if ep := strings.TrimSpace(c.Endpoint); ep != "" {
		return strings.TrimRight(ep, "/")
	}
	return EcloudDefaultEndpoint
}

// EcloudScope 素材 ID 的缓存隔离键。移动云的素材只在同一个 AccessKey 下可见，跨账号复用会
// 拿到别人（或已失效）的 asset://。
func EcloudScope(cfg EcloudConfig) string {
	return cfg.endpoint() + "|" + ProtocolEcloud + "|" + strings.TrimSpace(cfg.AccessKey)
}

type EcloudClient struct {
	cfg  EcloudConfig
	http *http.Client
	now  func() time.Time
}

func NewEcloudClient(cfg EcloudConfig, httpClient *http.Client) *EcloudClient {
	return &EcloudClient{cfg: cfg, http: httpClient, now: time.Now}
}

// ecloudEnvelope 移动云所有接口的统一信封：state=OK/ERROR + errorCode/errorMessage + body。
type ecloudEnvelope struct {
	RequestID    string          `json:"requestId"`
	State        string          `json:"state"`
	ErrorCode    string          `json:"errorCode"`
	ErrorMessage string          `json:"errorMessage"`
	Body         json.RawMessage `json:"body"`
}

func (c *EcloudClient) call(ctx context.Context, action, method, path string, in, out any) error {
	var payload []byte
	if in != nil {
		b, err := common.Marshal(in)
		if err != nil {
			return err
		}
		payload = b
	}

	endpoint := c.cfg.endpoint()
	req, err := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.sign(req, path)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return err
	}

	var env ecloudEnvelope
	decodeErr := common.Unmarshal(raw, &env)
	// state 为空说明这不是移动云的响应（比如把方舟地址或别的网关配进来了）。这种既不是
	// 业务错误也不是网络错误，单列出来让报错直接指向「协议选错了」。
	//
	// 5xx 除外：那是网关/代理的 HTML 错误页，跟协议无关，判成「协议选错」只会把排查带偏。
	if decodeErr != nil || env.State == "" {
		if resp.StatusCode < 500 {
			return &ProtocolMismatchError{Protocol: ProtocolEcloudLabel, Endpoint: endpoint, Sample: truncate(string(raw), 300)}
		}
		return &APIError{Action: action, HTTPStatus: resp.StatusCode, Message: truncate(string(raw), 300)}
	}
	if env.State != "OK" {
		return &APIError{Action: action, HTTPStatus: resp.StatusCode, Code: env.ErrorCode, Message: env.ErrorMessage}
	}
	if resp.StatusCode >= 300 {
		return &APIError{Action: action, HTTPStatus: resp.StatusCode, Message: truncate(string(raw), 300)}
	}
	if out == nil {
		return nil
	}
	return common.Unmarshal(env.Body, out)
}

// sign 把公共参数与 Signature 写进 query。移动云的签名只覆盖 query，body 不参与。
func (c *EcloudClient) sign(req *http.Request, path string) {
	params := map[string]string{
		"AccessKey":        strings.TrimSpace(c.cfg.AccessKey),
		"SignatureMethod":  "HmacSHA1",
		"SignatureVersion": "V2.0",
		"SignatureNonce":   ecloudNonce(),
		"Timestamp":        c.now().In(ecloudZone).Format(ecloudTimestampFormat),
	}
	signature := ecloudSignature(req.Method, path, params, strings.TrimSpace(c.cfg.SecretKey))

	q := req.URL.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	q.Set("Signature", signature)
	req.URL.RawQuery = q.Encode()
}

// ecloudSignature 移动云统一签名：
//
//	canonical = 除 Signature 外的参数（含接口自定义参数）按名排序，k=v 百分号编码后以 & 连接
//	toSign    = METHOD \n percentEncode(path) \n hex(sha256(canonical))
//	signature = hex(HMAC-SHA1("BC_SIGNATURE&"+SecretKey, toSign))
//
// 算法由官方示例向量固定住（见 ecloud_test.go），改动这里必须同步改那条用例。
func ecloudSignature(method, path string, params map[string]string, secretKey string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "Signature" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, ecloudPercentEncode(k)+"="+ecloudPercentEncode(params[k]))
	}
	canonical := strings.Join(pairs, "&")

	digest := sha256.Sum256([]byte(canonical))
	toSign := method + "\n" + ecloudPercentEncode(path) + "\n" + hex.EncodeToString(digest[:])

	mac := hmac.New(sha1.New, []byte(ecloudSigningPrefix+secretKey))
	mac.Write([]byte(toSign))
	return hex.EncodeToString(mac.Sum(nil))
}

// ecloudPercentEncode 移动云的编码规则：A-Za-z0-9 与 -_.~ 不编码，空格编码成 %20（不是 +）。
func ecloudPercentEncode(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	e = strings.ReplaceAll(e, "*", "%2A")
	e = strings.ReplaceAll(e, "%7E", "~")
	return e
}

func ecloudNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

// FindGroup 按名称找 AIGC 素材组。接口的 groupName 是模糊搜索，这里取精确同名的那个。
func (c *EcloudClient) FindGroup(ctx context.Context, name string) (string, error) {
	var out struct {
		Data []struct {
			GroupID   string `json:"groupId"`
			GroupName string `json:"groupName"`
		} `json:"data"`
	}
	err := c.call(ctx, "查询素材组列表", http.MethodPost, ecloudAssetGroupPath+"/query", map[string]any{
		"pageNo":    1,
		"pageSize":  100,
		"groupType": groupTypeAIGC,
		"groupName": name,
	}, &out)
	if err != nil {
		return "", err
	}
	for _, g := range out.Data {
		if g.GroupName == name {
			return g.GroupID, nil
		}
	}
	return "", nil
}

func (c *EcloudClient) CreateGroup(ctx context.Context, name, description string) (string, error) {
	var body json.RawMessage
	err := c.call(ctx, "创建素材组", http.MethodPost, ecloudAssetGroupPath, map[string]any{
		"groupName":   name,
		"description": description,
		"groupType":   groupTypeAIGC,
	}, &body)
	if err != nil {
		return "", err
	}
	return ecloudIDFromBody(body), nil
}

func (c *EcloudClient) CreateAsset(ctx context.Context, groupID, url, assetType, name string) (string, error) {
	var body json.RawMessage
	err := c.call(ctx, "创建素材", http.MethodPost, ecloudAssetPath, map[string]any{
		"groupId":   groupID,
		"assetName": name,
		"assetUrl":  url,
		"assetType": assetType,
	}, &body)
	if err != nil {
		return "", err
	}
	return ecloudIDFromBody(body), nil
}

func (c *EcloudClient) GetAsset(ctx context.Context, id string) (*Asset, error) {
	var out struct {
		AssetID      string `json:"assetId"`
		Status       string `json:"status"`
		ErrorMessage string `json:"errorMessage"`
	}
	if err := c.call(ctx, "查询素材详情", http.MethodGet, ecloudAssetPath+"/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	asset := &Asset{ID: out.AssetID, Status: ecloudAssetStatus(out.Status)}
	if asset.ID == "" {
		asset.ID = id
	}
	asset.Error.Message = out.ErrorMessage
	return asset, nil
}

// ecloudAssetStatus 移动云的状态是大写枚举（PROCESSING/ACTIVE/FAILED），方舟是首字母大写。
// Uploader 的轮询只认同名常量，不归一化的话就永远等不到 Active——一路轮询到超时，
// 失败素材也判不出来。
func ecloudAssetStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "ACTIVE":
		return StatusActive
	case "FAILED":
		return StatusFailed
	case "PROCESSING":
		return StatusProcessing
	}
	return status
}

// ecloudIDFromBody 创建类接口的 body 文档写的是「素材id」字符串，但素材组那边也可能包成
// {groupId}。两种都认；认不出就返回空串——让调用方按「空 ID」明确报错，而不是拿错的东西去建素材。
func ecloudIDFromBody(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := common.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var obj struct {
		GroupID string `json:"groupId"`
		ID      string `json:"id"`
	}
	if err := common.Unmarshal(raw, &obj); err == nil {
		if obj.GroupID != "" {
			return obj.GroupID
		}
		return obj.ID
	}
	return ""
}
