package controller

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/moderation"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 审核节点连通性测试（docs/content-moderation-design.md §8.4 P1）。
//
// 存在的理由：填错一个字符——地址少个端口、模型名和 GPUStack 里的对不上、Key 过期——
// 在没有这个接口时只能等审核真正开始跑才发现，而那时的表现是「审核报错」或
// fail-close 下的全站拒绝，排查要翻服务日志。这里让运营在保存前就看到结果。

type testModerationEndpointRequest struct {
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key"`
	TimeoutMS int    `json:"timeout_ms"`
}

// TestModerationEndpoint 用一段固定的无害文本打一次真实判定。
//
// 不打 /v1/models 而打真实的判定调用：/v1/models 通了只说明地址和 Key 对，
// 说明不了「这个模型能不能给出可解析的判定」——而后者才是审核链路真正依赖的。
// 模型名填错、部署的不是 guard 模型、chat template 不对，全都只有真调一次才暴露。
func TestModerationEndpoint(c *gin.Context) {
	var req testModerationEndpointRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiErrorMsg(c, "请求参数有误")
		return
	}
	if strings.TrimSpace(req.BaseURL) == "" || strings.TrimSpace(req.Model) == "" {
		common.ApiErrorMsg(c, "地址与模型名不能为空")
		return
	}
	if req.TimeoutMS <= 0 {
		req.TimeoutMS = 5000
	}

	// api_key 留空表示沿用已保存的那条（前端回显时密钥被抹掉，见 RedactModerationEndpoints）。
	// 不这样处理的话，运营点一次「测试」就得把密钥重新贴一遍。
	apiKey := req.APIKey
	if apiKey == "" && req.Name != "" {
		for _, e := range system_setting.GetModerationSettings().Endpoints {
			if e.Name == req.Name {
				apiKey = e.GetAPIKey()
				break
			}
		}
	}

	start := time.Now()
	result := moderation.TestEndpoint(c, req.BaseURL, req.Model, apiKey, req.TimeoutMS)
	latency := time.Since(start).Milliseconds()

	if result.Err != nil {
		common.ApiErrorMsg(c, result.Err.Error())
		return
	}
	// 测过是活的就把冻结解掉：管理员刚验证过，没理由让生产流量继续绕开它。
	if result.ParsedOK && req.Name != "" {
		moderation.ClearEndpointFreeze(req.Name)
	}
	common.ApiSuccess(c, gin.H{
		"latency_ms": latency,
		// 把模型原样的输出回给前端。判定文本本身就是最好的诊断信息：
		// 看到 "Safety: Safe" 就知道整条链路通了，看到别的就知道部署的不是 guard 模型。
		"raw":       result.Raw,
		"parsed_ok": result.ParsedOK,
	})
}
