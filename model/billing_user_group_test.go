package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGetBillingUserGroup_NonSubAccount 普通账号（ParentUserId <= 0）直接用自己的
// 分组，不触发任何父账号查询。
//
// 这条覆盖的是绝大多数请求的路径——现网 152 个用户里只有企业子账号会走另一个分支。
// 若它被改成无条件查父账号，每次 /api/pricing 都会多一次缓存/DB 往返，而且
// ParentUserId=0 查不到用户时会退回自身分组，症状是「慢但结果正确」，很难归因。
func TestGetBillingUserGroup_NonSubAccount(t *testing.T) {
	t.Run("普通账号用自己的分组", func(t *testing.T) {
		u := &UserBase{Id: 1, Group: "vip", ParentUserId: 0}
		require.Equal(t, "vip", GetBillingUserGroup(u))
	})

	t.Run("负数 ParentUserId 视为普通账号", func(t *testing.T) {
		u := &UserBase{Id: 2, Group: "default", ParentUserId: -1}
		require.Equal(t, "default", GetBillingUserGroup(u))
	})

	t.Run("nil 返回空串而非 panic", func(t *testing.T) {
		require.Equal(t, "", GetBillingUserGroup(nil))
	})
}
