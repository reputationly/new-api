package controller

import (
	"bytes"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
)

// AggregateModelDryRun 聚合模型配置的干跑校验(超管)。
//
// 只做静态校验:不发起任何真实调用,不消耗 GPU、不计费、秒回。它与体验区**不重叠** ——
// 体验区验证效果(模板写得好不好、片子行不行),这里验证配置(模型名能不能路由、分组继承
// 出来是谁、超分模型有没有超分能力)。后者体验区永远给不了,而它们错了全都不报错:
// 要么真实调用时才失败,要么静默少跑一段、默默出差档。
//
// 接受**尚未保存**的配置体,让运营在点保存之前就能看到问题;传空则校验当前已保存的配置。
func AggregateModelDryRun(c *gin.Context) {
	var body struct {
		Models []*common.AggregateModel `json:"models"`
	}

	// 按**实际字节**判断有没有提交配置体,不看 Content-Length:chunked 传输
	// (curl -d @-、部分 SDK)下 Go 把 ContentLength 置为 -1,照它判断会跳过解析、
	// 转而校验已保存的配置并返回一个看起来正常的 200 —— 运营以为校验的是编辑器里
	// 那份未保存的配置。那正是本接口要消除的「不报错、给错答案」。
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "读取请求体失败: " + err.Error(),
		})
		return
	}
	submitted := len(bytes.TrimSpace(raw)) > 0
	if submitted {
		if err := common.Unmarshal(raw, &body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "配置格式非法: " + err.Error(),
			})
			return
		}
	}

	// 回落到已保存配置**只发生在完全没提交 body 时**。不能用 len(Models)==0 判断:
	// 那样「把最后一条配置删掉后点校验」会去校验旧配置,报一堆用户刚刚删掉的问题。
	models := body.Models
	if !submitted {
		// 这里刻意**不用** GetAggregateModels():它是运行时视图,坏配置返回空表、
		// enabled:false 的条目被丢掉。拿它校验会让一份存坏了的配置显示成
		// 「没什么可校验的,一切正常」—— 而这个接口存在的全部理由就是消除这种假绿灯。
		// 走原始串 + 完整解析,才能把"没有配置"和"配置坏了"分开,也才与提交 body 的
		// 路径口径一致(那条路同样会校验 enabled:false 的条目)。
		raw := common.RawAggregateModelConfig()
		parsed, parseErr := common.ParseAggregateModelList(raw)
		if parseErr != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"data": gin.H{
					"passed":  false,
					"results": []any{},
					"message": "已保存的聚合模型配置无法解析,当前全部聚合模型均不生效: " + parseErr.Error(),
				},
			})
			return
		}
		models = parsed
		// 解析结果按名字排序:原始 JSON 的顺序由运营编辑决定,但回落路径要稳定输出,
		// 否则配置页列表每次点校验都在跳,两次结果也无法比对。
		sort.Slice(models, func(i, j int) bool {
			return strings.TrimSpace(models[i].Name) < strings.TrimSpace(models[j].Name)
		})
	}

	results := service.DryRunAggregateConfig(models)
	allPassed := true
	for _, r := range results {
		if !r.Passed {
			allPassed = false
			break
		}
	}
	resp := gin.H{
		"passed":  allPassed,
		"results": results,
	}
	// 零条结果时必须说清楚是"没有可校验的东西",而不是让调用方把空结果读成"全部通过"。
	if len(results) == 0 {
		resp["message"] = "当前没有配置任何聚合模型,无可校验项"
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    resp,
	})
}
