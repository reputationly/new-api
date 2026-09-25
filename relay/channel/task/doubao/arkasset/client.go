// Package arkasset 对接方舟素材库（Action 协议，Version=2024-01-01），把客户上传的参考图 /
// 视频先入库、再以 asset://<id> 调视频生成——Seedance 2.x 直传含人脸的素材会被输入审核拦截
// （InputImageSensitiveContentDetected.PrivacyInformation），入库后按虚拟人像素材使用。
//
// 鉴权：火山官方素材接口只收 AK/SK 签名；界云等兼容网关另外接受 Bearer API Key。
// 配了 AK/SK 就签名，否则回落到渠道自身的 API Key。
package arkasset

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	apiVersion    = "2024-01-01"
	groupTypeAIGC = "AIGC"

	AssetTypeImage = "Image"
	AssetTypeVideo = "Video"

	StatusActive     = "Active"
	StatusFailed     = "Failed"
	StatusProcessing = "Processing"

	maxResponseBytes = 1 << 20
)

type Config struct {
	Endpoint    string // 素材 Action 的根地址，如 https://ark.cn-beijing.volcengineapi.com
	AccessKey   string
	SecretKey   string
	APIKey      string // AK/SK 缺省时的 Bearer 回落
	ProjectName string
}

func (c Config) signing() bool {
	return c.AccessKey != "" && c.SecretKey != ""
}

// APIError 是素材接口 ResponseMetadata.Error 里的业务错误（或无错误体的非 2xx）。
type APIError struct {
	Action     string
	HTTPStatus int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("ark asset %s failed (http %d): %s %s", e.Action, e.HTTPStatus, e.Code, e.Message)
}

type Client struct {
	cfg  Config
	http *http.Client
	now  func() time.Time
}

func NewClient(cfg Config, httpClient *http.Client) *Client {
	if cfg.ProjectName == "" {
		cfg.ProjectName = "default"
	}
	return &Client{cfg: cfg, http: httpClient, now: time.Now}
}

type envelope struct {
	ResponseMetadata struct {
		RequestID string `json:"RequestId"`
		Error     *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error"`
	} `json:"ResponseMetadata"`
	Result json.RawMessage `json:"Result"`
}

func (c *Client) call(ctx context.Context, action string, in, out any) error {
	body, err := common.Marshal(in)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(c.cfg.Endpoint, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		endpoint+"/?Action="+action+"&Version="+apiVersion, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.signing() {
		signRequest(req, body, c.cfg.AccessKey, c.cfg.SecretKey, c.now())
	} else {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return err
	}

	var env envelope
	if err := common.Unmarshal(raw, &env); err != nil {
		return &APIError{Action: action, HTTPStatus: resp.StatusCode, Message: truncate(string(raw), 300)}
	}
	// 业务错误可能伴随 HTTP 200，必须看 ResponseMetadata.Error。
	if e := env.ResponseMetadata.Error; e != nil && (e.Code != "" || e.Message != "") {
		return &APIError{Action: action, HTTPStatus: resp.StatusCode, Code: e.Code, Message: e.Message}
	}
	if resp.StatusCode >= 300 {
		return &APIError{Action: action, HTTPStatus: resp.StatusCode, Message: truncate(string(raw), 300)}
	}
	if out == nil {
		return nil
	}
	return common.Unmarshal(env.Result, out)
}

type assetGroup struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

// FindGroup 按名称找 AIGC 素材组。接口的 Name 过滤是模糊匹配，这里取精确同名的那个。
func (c *Client) FindGroup(ctx context.Context, name string) (string, error) {
	var out struct {
		Items []assetGroup `json:"Items"`
	}
	err := c.call(ctx, "ListAssetGroups", map[string]any{
		"Filter":      map[string]any{"GroupType": groupTypeAIGC, "Name": name},
		"PageNumber":  1,
		"PageSize":    100,
		"ProjectName": c.cfg.ProjectName,
	}, &out)
	if err != nil {
		return "", err
	}
	for _, g := range out.Items {
		if g.Name == name {
			return g.ID, nil
		}
	}
	return "", nil
}

func (c *Client) CreateGroup(ctx context.Context, name, description string) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	err := c.call(ctx, "CreateAssetGroup", map[string]any{
		"Name":        name,
		"Description": description,
		"GroupType":   groupTypeAIGC,
		"ProjectName": c.cfg.ProjectName,
	}, &out)
	return out.ID, err
}

func (c *Client) CreateAsset(ctx context.Context, groupID, url, assetType, name string) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	err := c.call(ctx, "CreateAsset", map[string]any{
		"GroupId":     groupID,
		"URL":         url,
		"Name":        name,
		"AssetType":   assetType,
		"ProjectName": c.cfg.ProjectName,
	}, &out)
	return out.ID, err
}

type Asset struct {
	ID     string `json:"Id"`
	Status string `json:"Status"`
	Error  struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	} `json:"Error"`
}

func (c *Client) GetAsset(ctx context.Context, id string) (*Asset, error) {
	var out Asset
	if err := c.call(ctx, "GetAsset", map[string]any{"Id": id, "ProjectName": c.cfg.ProjectName}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
