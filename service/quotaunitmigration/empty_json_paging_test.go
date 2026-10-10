package quotaunitmigration

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestJSONPagingDoesNotStopAtEmptyRows(t *testing.T) {
	cases := map[string]func(id int) bool{
		"sparse_empty":      func(id int) bool { return id%7 == 0 },
		"consecutive_empty": func(id int) bool { return id >= 100 && id < 700 },
	}
	for name, isEmpty := range cases {
		t.Run(name, func(t *testing.T) {
			db := openTestDB(t)
			seedOption(t, db, "USDExchangeRate", "7.3")
			logs := make([]model.Log, 0, 2000)
			for id := 1; id <= 2000; id++ {
				other := fmt.Sprintf(`{"fee_quota":%d}`, fUsed)
				if isEmpty(id) {
					other = ""
				}
				logs = append(logs, model.Log{Id: id, UserId: 1, Type: model.LogTypeConsume, Other: other, CreatedAt: int64(id)})
			}
			require.NoError(t, db.CreateInBatches(logs, 500).Error)

			_, err := runMigration(t, ModeApply)
			require.NoError(t, err)

			want := fmt.Sprintf(`{"fee_quota":%d}`, expectScaled(fUsed))
			var notScaled []int
			var rows []model.Log
			require.NoError(t, db.Order("id").Find(&rows).Error)
			for _, r := range rows {
				if r.Other != "" && r.Other != want {
					notScaled = append(notScaled, r.Id)
				}
			}
			require.Empty(t, notScaled, "未缩放的行数 %d，首个 id %v", len(notScaled), firstN(notScaled, 5))
		})
	}
}

func firstN(ids []int, n int) []int {
	if len(ids) < n {
		return ids
	}
	return ids[:n]
}
