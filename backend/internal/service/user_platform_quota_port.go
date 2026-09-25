package service

import (
	"context"
	"errors"
	"time"
)

// ErrUserPlatformQuotaNotFound service 层 sentinel：quota 记录不存在。
// adapter 将 repository.ErrUserPlatformQuotaNotFound 包装为此错误，
// handler 只需引用 service 包，无需直接依赖 repository 包。
var ErrUserPlatformQuotaNotFound = errors.New("user platform quota not found")

// ErrUserPlatformQuotaFKViolation service 层 sentinel：批量快照写入命中了 user_id 不在 users 表的记录。
// adapter 负责将 repository 层同名 sentinel 包装为此错误。
var ErrUserPlatformQuotaFKViolation = errors.New("user platform quota snapshot FK violation")

// UserPlatformQuotaSnapshot 是 service 层 flusher 向 DB 写入快照时使用的传输结构。
// 字段语义与 repository.UserPlatformQuotaSnapshot 完全对应，由 adapter 负责转换。
type UserPlatformQuotaSnapshot struct {
	UserID             int64
	Platform           string
	DailyUsageUSD      float64
	WeeklyUsageUSD     float64
	MonthlyUsageUSD    float64
	DailyWindowStart   time.Time
	WeeklyWindowStart  time.Time
	MonthlyWindowStart time.Time
}

// UserPlatformQuotaRecord service 层传输结构体（与 repository 层解耦）。
type UserPlatformQuotaRecord struct {
	UserID          int64
	Platform        string
	DailyLimitUSD   *float64
	WeeklyLimitUSD  *float64
	MonthlyLimitUSD *float64
	DailyUsageUSD   float64
	WeeklyUsageUSD  float64
	MonthlyUsageUSD float64
	// 窗口起始时间（可选，用于未来 reset 校验）
	DailyWindowStart   *time.Time
	WeeklyWindowStart  *time.Time
	MonthlyWindowStart *time.Time
}

// HasAnyLimit 报告该记录是否至少配置了一档限额（0 也算配置）。
// 该方法只描述限额配置状态，不决定记录生命周期。三档全空表示不限额，
// 记录仍可持久化并继续保存、累加和重置 usage。
func (r UserPlatformQuotaRecord) HasAnyLimit() bool {
	return r.DailyLimitUSD != nil || r.WeeklyLimitUSD != nil || r.MonthlyLimitUSD != nil
}

// UserPlatformQuotaRepository 定义 service 层所需的 user × platform quota 数据访问端口。
// repository 包的 userPlatformQuotaRepository 实现此接口。
type UserPlatformQuotaRepository interface {
	// GetByUserPlatform 查询单条配额记录，未找到时返回 (nil, nil)。
	GetByUserPlatform(ctx context.Context, userID int64, platform string) (*UserPlatformQuotaRecord, error)
	// BulkInsertInitial 幂等批量插入全部初始记录（包括三档全空的不限额记录）；
	// 冲突时只补既有记录中为 NULL 的限额档位，并保留累计 usage/window。
	BulkInsertInitial(ctx context.Context, records []UserPlatformQuotaRecord) error
	// IncrementUsageWithReset 原子地累加用量，若窗口已过期则先重置再累加；
	// 记录不存在时创建限额全 NULL 的不限额行并写入本次 usage。
	IncrementUsageWithReset(ctx context.Context, userID int64, platform string, cost float64, now time.Time) error
	// ListByUser 查询用户的所有平台配额记录。
	ListByUser(ctx context.Context, userID int64) ([]UserPlatformQuotaRecord, error)
	// UpsertForUser 全量替换该用户所有平台限额配置（事务内）：
	//   1. 只软删除未在 records 中出现的 active 行
	//   2. 对每条 record（包括三档全空）：UPDATE 已存在的 active 行；未命中时 INSERT
	//   3. 仅改 *_limit_usd + updated_at，保留既有 *_usage_usd / *_window_start。
	// 清空某平台的全部限额表示不限额；只要该平台仍在 records 中，行和累计用量就保留。
	// records 为空时软删除该用户的全部 active 行。
	UpsertForUser(ctx context.Context, userID int64, records []UserPlatformQuotaRecord) error
	// ResetExpiredWindow 重置指定窗口（"daily"|"weekly"|"monthly"）的用量与起始时间。
	// 未命中活跃记录时返回（service-side wrapper of repository.ErrUserPlatformQuotaNotFound）。
	ResetExpiredWindow(ctx context.Context, userID int64, platform string, window string, newStart time.Time) error
	// BatchSnapshotUsage 以 UPSERT 绝对值覆盖整批 usage 快照；不存在或仅有软删记录的
	// (user,platform) 会创建限额全 NULL 的新 active 行。
	BatchSnapshotUsage(ctx context.Context, snapshots []UserPlatformQuotaSnapshot, now time.Time) error
}
