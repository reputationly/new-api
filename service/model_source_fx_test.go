package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCFETSSyncAndFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	original := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = original })
	require.NoError(t, db.AutoMigrate(&model.ModelSourceFXQuote{}, &model.ModelSourceFXStatus{}))

	var mode atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case cfetsHistory:
			if r.Method != http.MethodPost || r.URL.Query().Get("currency") != "USD/CNY" || r.URL.Query().Get("startDate") != "2026-10-26" {
				t.Error("unexpected CFETS history request")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, `{"head":{"rep_code":"200"},"data":{"searchlist":["USD/CNY"]},"records":[{"date":"2026-11-23","values":["6.7111"]},{"date":"2026-11-24","values":["6.7100"]}]}`)
		case cfetsLatest:
			if r.Method != http.MethodGet {
				t.Error("unexpected CFETS latest method")
			}
			switch mode.Load() {
			case 0:
				fmt.Fprint(w, `{"head":{"rep_code":"200"},"data":{"lastDate":"2026-11-24 9:15"},"records":[{"vrtEName":"USD/CNY","price":"6.7211"}]}`)
			case 1:
				w.WriteHeader(http.StatusServiceUnavailable)
			default:
				fmt.Fprint(w, `{"head":{"rep_code":"200"},"data":{"lastDate":"2026-11-24 9:15"},"records":[{"vrtEName":"USD/CNY","price":"NaN"}]}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := cfetsClient{client: server.Client(), origin: server.URL}
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, cfetsLocation)
	require.NoError(t, client.sync(ctx, now))
	success := now.Add(time.Minute)
	require.NoError(t, client.sync(ctx, success))
	mode.Store(1)
	require.ErrorContains(t, client.sync(ctx, now.Add(2*time.Minute)), "HTTP 503")
	quotes, status, err := model.ReadModelSourceFX(ctx, "2026-10-26", "2026-11-24")
	require.NoError(t, err)
	require.Len(t, quotes, 2)
	require.Equal(t, 6.7211, quotes[1].Rate)
	require.Equal(t, success.UnixMilli(), status.LastSuccess)
	require.Contains(t, status.Error, "HTTP 503")
	mode.Store(2)
	require.ErrorContains(t, client.sync(ctx, now.Add(3*time.Minute)), "有效 USD/CNY")
	mode.Store(0)
	require.NoError(t, client.sync(ctx, now.Add(4*time.Minute)))
	_, status, err = model.ReadModelSourceFX(ctx, "2026-10-26", "2026-11-24")
	require.NoError(t, err)
	require.Empty(t, status.Error)
}

func TestCFETSPublishDays(t *testing.T) {
	tuesday := time.Date(2026, 11, 24, 9, 30, 0, 0, cfetsLocation)
	require.Equal(t, 2, CFETSMissingPublishDays("2026-11-19", tuesday.Add(-time.Second)))
	require.Equal(t, 3, CFETSMissingPublishDays("2026-11-19", tuesday))
	require.Equal(t, 2, CFETSMissingPublishDays("2026-11-20", tuesday))
	friday := tuesday.AddDate(0, 0, 3)
	require.Equal(t, friday.AddDate(0, 0, 3), nextCFETSRun(friday))
}
