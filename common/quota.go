package common

func GetTrustQuota() int {
	return 73 * QuotaPerYuan
}

const MinAffiliateTransferQuota = 7_300_000_000

func MaxTokenQuota() int64 {
	return 7_300_000_000_000_000_000
}
