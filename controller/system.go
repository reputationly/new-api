/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

package controller

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// watchtowerPlaceholderToken 是 docker-compose.yml 里 WATCHTOWER_API_TOKEN 的默认占位值。
//
// 必须单独识别它：那个变量的默认值**不是空字符串**而是这个占位串。如果把 compose 的 env
// 逐行搬进 K8s ConfigMap（迁移到 CCE 时很容易这么干），token 就"有值"了，
// 于是接口会一路走到「无法连接升级服务」而不是「升级服务未配置」——报错指错方向，
// 排查的人会去查 watchtower 为什么连不上，而真正的问题是它压根不该被启用。
const watchtowerPlaceholderToken = "CHANGE_ME_TO_RANDOM_TOKEN"

// watchtowerConfigured 判断本实例是否真的能通过 Watchtower 自升级。
// 空值 / 占位值都算未配置。供 /api/status 下发，让前端能据此隐藏「立即安装」。
func watchtowerConfigured() bool {
	token := os.Getenv("WATCHTOWER_API_TOKEN")
	apiURL := os.Getenv("WATCHTOWER_API_URL")
	return token != "" && token != watchtowerPlaceholderToken && apiURL != ""
}

// PerformUpdate triggers a Watchtower-based container image update.
// Requires WATCHTOWER_API_TOKEN and WATCHTOWER_API_URL to be set in the environment.
// POST /api/system/update
//
// 注意这个端点在 CCE 上是**不该可用**的：Pod 里没有 docker.sock，即使挂上，watchtower
// 绕过 Deployment 重建容器也会被 Kubernetes 拉回 Deployment 声明的镜像。迁 CCE 时应当
// 整行删除 WATCHTOWER_API_TOKEN / WATCHTOWER_API_URL（不是留空），升级改由 CCE 控制台的
// 滚动升级 + 回退承担。详见 docs/cce-migration-analysis.md ①。
func PerformUpdate(c *gin.Context) {
	userId := c.GetInt("id")

	// 判据和 /api/status 下发给前端的是同一个函数，避免两处条件漂移
	// （比如这里加了占位值判断、那里忘了加，前端就会显示一个点了必然报错的按钮）。
	if !watchtowerConfigured() {
		common.SysLog(fmt.Sprintf("system update failed: Watchtower not configured (user %d)", userId))
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "升级服务未配置，请在 docker-compose.yml 中设置 WATCHTOWER_API_TOKEN 和 WATCHTOWER_API_URL",
		})
		return
	}

	token := os.Getenv("WATCHTOWER_API_TOKEN")
	apiURL := os.Getenv("WATCHTOWER_API_URL")

	common.SysLog(fmt.Sprintf("user %d triggered system update via Watchtower", userId))

	url := fmt.Sprintf("%s/v1/update", apiURL)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, url, nil)
	if err != nil {
		common.SysLog(fmt.Sprintf("system update: failed to create request: %s (user %d)", err.Error(), userId))
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "创建升级请求失败: " + err.Error(),
		})
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		common.SysLog(fmt.Sprintf("system update: Watchtower connection failed: %s (user %d)", err.Error(), userId))
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "无法连接升级服务，请确认 Watchtower 容器正在运行",
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			body = []byte("(unable to read response)")
		}
		common.SysLog(fmt.Sprintf("system update: Watchtower returned %d: %s (user %d)", resp.StatusCode, string(body), userId))
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": fmt.Sprintf("升级服务返回错误 %d: %s", resp.StatusCode, string(body)),
		})
		return
	}

	common.SysLog(fmt.Sprintf("system update: command sent successfully (user %d)", userId))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "升级指令已发送，容器正在重建，请稍候...",
	})
}
