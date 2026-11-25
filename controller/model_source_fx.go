package controller

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetModelSourceFX(c *gin.Context) {
	ctx := c.Request.Context()
	now := time.Now()
	from, through := service.ModelSourceFXWindow(now)
	quotes, status, err := model.ReadModelSourceFX(ctx, from.Format(time.DateOnly), through.Format(time.DateOnly))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	baseline, changes, err := model.ReadModelSourceFXChanges(ctx, from)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	current, err := model.ReadModelSourceFXRate(ctx)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	latestDate := ""
	if len(quotes) != 0 {
		latestDate = quotes[len(quotes)-1].QuoteDate
	} else if status.LastSuccess > 0 {
		latest, err := model.ReadLatestModelSourceFXQuote(ctx, through.Format(time.DateOnly))
		if err != nil {
			common.ApiError(c, err)
			return
		}
		latestDate = latest.QuoteDate
	}
	lastChange := baseline
	if len(changes) != 0 {
		lastChange = &changes[len(changes)-1]
	}
	common.ApiSuccess(c, gin.H{
		"from": from.Unix(), "through": through.AddDate(0, 0, 1).Unix() - 1, "quotes": quotes,
		"status": status, "today": through.Format(time.DateOnly), "latest_date": latestDate,
		"stats_from": through.AddDate(0, 0, -29).Format(time.DateOnly),
		"baseline":   baseline, "changes": changes, "last_change": lastChange,
		"current_rate": current,
	})
}
