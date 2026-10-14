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

// watchtowerConfigured 判断本实例是否接了 Watchtower（页面内自升级）。
// 供 /api/status 下发，让前端据此决定「立即安装」按钮显不显示。
//
// 判据就是"两个变量都非空"。**不要**再去识别 compose 的默认值
// （CHANGE_ME_TO_RANDOM_TOKEN）并把它当成"没配"的哨兵值：docker-compose.yml 里
// new-api 和 watchtower 两个服务用的是同一个
// `${WATCHTOWER_API_TOKEN:-CHANGE_ME_TO_RANDOM_TOKEN}` 默认表达式，
// 那个串是**两边一致、开箱即用**的默认凭证，不是"未设置"。把它判成未配置，
// 会让按钮在 compose/BMS 部署上凭空消失（`/api/system/update` 直接 503），
// 而这条路径本来是好的。
//
// 部署形态的判断交给变量**在不在**：compose 部署两个变量都在（URL 在
// docker-compose.yml 里写死为 http://watchtower:8080，token 有默认值）；
// 迁到 CCE/K8s 时把这两行**整行删掉**（不是留空），这里就返回 false，前端自动隐藏按钮。
func watchtowerConfigured() bool {
	token := os.Getenv("WATCHTOWER_API_TOKEN")
	apiURL := os.Getenv("WATCHTOWER_API_URL")
	return token != "" && apiURL != ""
}

// PerformUpdate triggers a Watchtower-based container image update.
// Requires WATCHTOWER_API_TOKEN and WATCHTOWER_API_URL to be set in the environment.
// POST /api/system/update
//
// 注意这个端点在 CCE 上是**不该可用**的：Pod 里没有 docker.sock，即使挂上，watchtower
// 绕过 Deployment 重建容器也会被 Kubernetes 拉回 Deployment 声明的镜像。迁 CCE 时应当
// 整行删除 WATCHTOWER_API_TOKEN / WATCHTOWER_API_URL（不是留空）——这正是
// watchtowerConfigured() 的判据，删掉之后按钮自动消失，升级改由 CCE 控制台的
// 滚动升级 + 回退承担。详见 docs/cce-migration-analysis.md ①。
func PerformUpdate(c *gin.Context) {
	userId := c.GetInt("id")

	// 判据和 /api/status 下发给前端的是同一个函数，避免两处条件漂移
	// （一处改了、另一处忘了改，前端就会显示一个点了必然报错的按钮）。
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
