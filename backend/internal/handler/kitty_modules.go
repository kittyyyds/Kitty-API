package handler

import (
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// KittyModules aggregates all reconstructed Kitty custom handlers and background services
// cleanly on top of the upstream 0.2.24.3 base without tangling upstream wire graphs.
type KittyModules struct {
	SQLDB               *sql.DB
	EntClient           *ent.Client
	RedisClient         *redis.Client
	Config              *config.Config
	BillingExport       *admin.BillingExportHandler
	RechargeBonus       *KittyRechargeBonusHandler
	AdminRechargeBonus  *admin.KittyRechargeBonusHandler
	RechargeBonusExpiry *service.RechargeBonusExpiryService
}

func ProvideKittyModules(
	sqlDB *sql.DB,
	entClient *ent.Client,
	redisClient *redis.Client,
	cfg *config.Config,
	billingService *service.BillingService,
	billingCache *service.BillingCacheService,
	paymentService *service.PaymentService,
	rechargeBonusRepo service.RechargeBonusRepository,
) *KittyModules {
	if billingService != nil && sqlDB != nil {
		billingService.SetGlobalContextRules(service.NewGlobalContextRuleStore(sqlDB))
	}
	if paymentService != nil && rechargeBonusRepo != nil {
		paymentService.SetRechargeBonusRepo(rechargeBonusRepo)
	}
	expiry := service.NewRechargeBonusExpiryService(rechargeBonusRepo, billingCache, 5*time.Minute)
	expiry.Start()
	return &KittyModules{
		SQLDB:               sqlDB,
		EntClient:           entClient,
		RedisClient:         redisClient,
		Config:              cfg,
		BillingExport:       admin.NewBillingExportHandler(service.NewBillingExportService(sqlDB)),
		RechargeBonus:       NewKittyRechargeBonusHandler(paymentService),
		AdminRechargeBonus:  admin.NewKittyRechargeBonusHandler(service.NewKittyRechargeBonusAdminStore(sqlDB)),
		RechargeBonusExpiry: expiry,
	}
}
