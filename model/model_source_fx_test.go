package model

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReadModelSourceFXChanges(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	old := LOG_DB
	LOG_DB = db
	t.Cleanup(func() { LOG_DB = old })
	require.NoError(t, db.AutoMigrate(&Log{}))
	for _, fixture := range []struct {
		at            int64
		before, after string
	}{
		{5, `{"legacy":6.7}`, "6.8"},
		{20, "6.8", "7"},
		{30, "7", ""},
	} {
		entry, err := (OptionAudit{Username: "operator"}).log(map[string]string{modelSourceRateKey: fixture.before}, map[string]string{modelSourceRateKey: fixture.after})
		require.NoError(t, err)
		entry.CreatedAt = fixture.at
		require.NoError(t, db.Create(entry).Error)
	}
	baseline, changes, err := ReadModelSourceFXChanges(context.Background(), time.Unix(10, 0))
	require.NoError(t, err)
	require.NotNil(t, baseline)
	require.Equal(t, 6.8, *baseline.Rate)
	require.Equal(t, "operator", baseline.Actor)
	require.Len(t, changes, 2)
	require.Equal(t, 7.0, *changes[0].Rate)
	require.Nil(t, changes[1].Rate)
}
