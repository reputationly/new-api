package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	cfetsOrigin  = "https://www.chinamoney.com.cn"
	cfetsHistory = "/ags/ms/cm-u-bk-ccpr/CcprHisNew"
	cfetsLatest  = "/r/cms/www/chinamoney/data/fx/ccpr.json"
)

var cfetsLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func ModelSourceFXWindow(now time.Time) (time.Time, time.Time) {
	local := now.In(cfetsLocation)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, cfetsLocation)
	return today.AddDate(0, 0, -29), today
}

func cfetsPublishTime(day time.Time) time.Time {
	return day.Add(9*time.Hour + 30*time.Minute)
}

func cfetsWeekday(day time.Time) bool {
	return day.Weekday() != time.Saturday && day.Weekday() != time.Sunday
}

func nextCFETSRun(now time.Time) time.Time {
	_, day := ModelSourceFXWindow(now)
	for !cfetsWeekday(day) || !cfetsPublishTime(day).After(now) {
		day = day.AddDate(0, 0, 1)
	}
	return cfetsPublishTime(day)
}

type cfetsClient struct {
	client *http.Client
	origin string
}

func (c cfetsClient) request(ctx context.Context, method, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, nil)
	if err != nil {
		return errors.New("无法构造 CFETS 请求")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return errors.New("CFETS 网络请求失败或超时")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CFETS 响应 HTTP %d", resp.StatusCode)
	}
	const maxResponse = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse || common.Unmarshal(body, dst) != nil {
		return errors.New("CFETS 响应格式无效")
	}
	return nil
}

type cfetsHead struct {
	Code string `json:"rep_code"`
}

func parseCFETSQuote(date, amount string, from, through time.Time) (model.ModelSourceFXQuote, bool) {
	day, err := time.ParseInLocation(time.DateOnly, date, cfetsLocation)
	rate, rateErr := strconv.ParseFloat(amount, 64)
	if err != nil || rateErr != nil || !(rate > 0) || math.IsInf(rate, 0) || day.Before(from) || day.After(through) {
		return model.ModelSourceFXQuote{}, false
	}
	return model.ModelSourceFXQuote{Pair: model.CFETSPair, QuoteDate: date, Rate: rate}, true
}

func (c cfetsClient) fetch(ctx context.Context, now time.Time) ([]model.ModelSourceFXQuote, error) {
	from, through := ModelSourceFXWindow(now)
	query := url.Values{
		"startDate": {from.Format(time.DateOnly)}, "endDate": {through.Format(time.DateOnly)},
		"currency": {model.CFETSPair}, "pageNum": {"1"}, "pageSize": {"40"},
	}
	var history struct {
		Head cfetsHead `json:"head"`
		Data struct {
			SearchList []string `json:"searchlist"`
		} `json:"data"`
		Records []struct {
			Date   string   `json:"date"`
			Values []string `json:"values"`
		} `json:"records"`
	}
	if err := c.request(ctx, http.MethodPost, cfetsHistory+"?"+query.Encode(), &history); err != nil {
		return nil, err
	}
	if history.Head.Code != "200" || len(history.Data.SearchList) != 1 || history.Data.SearchList[0] != model.CFETSPair || history.Records == nil {
		return nil, errors.New("CFETS 历史中间价响应缺少 USD/CNY 数据")
	}
	byDate := make(map[string]model.ModelSourceFXQuote, len(history.Records)+1)
	for _, row := range history.Records {
		if len(row.Values) != 1 {
			return nil, errors.New("CFETS 历史中间价栏位无效")
		}
		quote, ok := parseCFETSQuote(row.Date, row.Values[0], from, through)
		if !ok {
			return nil, errors.New("CFETS 历史中间价日期或价格无效")
		}
		byDate[quote.QuoteDate] = quote
	}
	var latest struct {
		Head cfetsHead `json:"head"`
		Data struct {
			LastDate string `json:"lastDate"`
		} `json:"data"`
		Records []struct {
			Pair  string `json:"vrtEName"`
			Price string `json:"price"`
		} `json:"records"`
	}
	if err := c.request(ctx, http.MethodGet, cfetsLatest, &latest); err != nil {
		return nil, err
	}
	date, _, _ := strings.Cut(latest.Data.LastDate, " ")
	if latest.Head.Code == "200" {
		for _, row := range latest.Records {
			if row.Pair != model.CFETSPair {
				continue
			}
			if quote, ok := parseCFETSQuote(date, row.Price, time.Time{}, through); ok {
				byDate[quote.QuoteDate] = quote
				quotes := make([]model.ModelSourceFXQuote, 0, len(byDate))
				for _, quote := range byDate {
					quotes = append(quotes, quote)
				}
				return quotes, nil
			}
			break
		}
	}
	return nil, errors.New("CFETS 最新中间价响应缺少有效 USD/CNY 数据")
}

func (c cfetsClient) sync(ctx context.Context, now time.Time) error {
	quotes, err := c.fetch(ctx, now)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	failure := ""
	if err != nil {
		failure = err.Error()
	}
	if saveErr := model.SaveModelSourceFXResult(ctx, now, quotes, failure); saveErr != nil {
		return fmt.Errorf("保存 CFETS 同步结果失败: %w", saveErr)
	}
	return err
}

func waitCFETS(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func RunModelSourceFX(ctx context.Context) {
	c := cfetsClient{
		origin: cfetsOrigin,
		client: &http.Client{
			Timeout:       12 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	for ctx.Err() == nil {
		for attempt := 0; attempt < 3; attempt++ {
			if err := c.sync(ctx, time.Now()); err != nil {
				if ctx.Err() != nil {
					return
				}
				common.SysLog(err.Error())
			} else {
				break
			}
			if attempt < 2 && !waitCFETS(ctx, time.Duration(attempt+1)*5*time.Minute) {
				return
			}
		}
		if !waitCFETS(ctx, time.Until(nextCFETSRun(time.Now()))) {
			return
		}
	}
}
