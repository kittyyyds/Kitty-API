package handler

import (
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/qqbot"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// KittyModules aggregates all reconstructed Kitty custom handlers and background services
// cleanly on top of the upstream 0.2.24.3 base without tangling upstream wire graphs.
type KittyModules struct {
	SQLDB                  *sql.DB
	EntClient              *ent.Client
	RedisClient            *redis.Client
	Config                 *config.Config
	BillingExport          *admin.BillingExportHandler
	RechargeBonus          *KittyRechargeBonusHandler
	AdminRechargeBonus     *admin.KittyRechargeBonusHandler
	RechargeBonusExpiry    *service.RechargeBonusExpiryService
	SupportTicket          *SupportTicketHandler
	BillingPreview         *BillingPreviewHandler
	ChannelMonitorV3       *ChannelMonitorV3Handler
	ChannelMonitorV3Prober *service.ChannelMonitorV3Prober
	ChannelMonitorV2Candy  *service.ChannelMonitorV2CandyRunner
	IntelligenceScheduler  *service.KittyIntelligenceScheduler
	AccountPerformance     *service.AccountPerformanceService
	UserCleanup            *admin.UserCleanupHandler
	QQBotService           *qqbot.Service
	QQBot                  *QQBotHandler
}

func ProvideKittyModules(
	sqlDB *sql.DB,
	entClient *ent.Client,
	redisClient *redis.Client,
	cfg *config.Config,
	secretEncryptor service.SecretEncryptor,
	settingService *service.SettingService,
	emailService *service.EmailService,
	adminService service.AdminService,
	userRepo service.UserRepository,
	groupRepo service.GroupRepository,
	accountRepo service.AccountRepository,
	channelRepo service.ChannelRepository,
	userGroupRateRepo service.UserGroupRateRepository,
	apiKeyService *service.APIKeyService,
	authCacheInvalidator service.APIKeyAuthCacheInvalidator,
	channelService *service.ChannelService,
	channelMonitorV2Service *service.ChannelMonitorV2Service,
	billingService *service.BillingService,
	billingCache *service.BillingCacheService,
	modelPricingResolver *service.ModelPricingResolver,
	paymentService *service.PaymentService,
	balanceNotifyService *service.BalanceNotifyService,
	upstreamBillingProbe *service.UpstreamBillingProbeService,
	rechargeBonusRepo service.RechargeBonusRepository,
	supportTicketRepo service.SupportTicketRepository,
	channelMonitorV3Repo service.ChannelMonitorV3Repository,
	gatewayService *service.GatewayService,
	openaiGatewayService *service.OpenAIGatewayService,
	concurrencyService *service.ConcurrencyService,
	accountTestService *service.AccountTestService,
	adminChannelHandler *admin.ChannelHandler,
	adminAccountHandler *admin.AccountHandler,
	httpUpstream service.HTTPUpstream,
	opsService *service.OpsService,
) *KittyModules {
	if billingService != nil && sqlDB != nil {
		billingService.SetGlobalContextRules(service.NewGlobalContextRuleStore(sqlDB))
	}
	if paymentService != nil && rechargeBonusRepo != nil {
		paymentService.SetRechargeBonusRepo(rechargeBonusRepo)
	}
	if upstreamBillingProbe != nil {
		if sqlDB != nil {
			upstreamBillingProbe.SetNewAPIAuthorization(
				repository.NewNewAPIAuthorizationRepository(sqlDB),
				secretEncryptor,
				cfg != nil && cfg.Totp.EncryptionKeyConfigured,
			)
		} else {
			upstreamBillingProbe.SetNewAPIEncryptor(secretEncryptor, cfg != nil && cfg.Totp.EncryptionKeyConfigured)
		}
		upstreamBillingProbe.SetProviderHub(channelService)
	}
	if setter, ok := httpUpstream.(interface {
		SetOpenAIResponseHeaderTimeoutProvider(repository.OpenAIResponseHeaderTimeoutProvider)
	}); ok && settingService != nil {
		setter.SetOpenAIResponseHeaderTimeoutProvider(settingService)
	}
	if concurrencyService != nil {
		if pauseReader, ok := accountRepo.(service.PerformancePauseReader); ok {
			concurrencyService.SetPerformancePauseReader(pauseReader)
		}
	}
	if channelService != nil {
		channelService.SetProviderHubProbe(upstreamBillingProbe)
	}
	if adminChannelHandler != nil {
		adminChannelHandler.SetProviderHubAdmin(adminService)
		if adminAccountHandler != nil {
			adminChannelHandler.ConfigureCS1Providers(adminAccountHandler, upstreamBillingProbe)
		}
	}

	runBG := cfg == nil || cfg.RunMode == "" || cfg.RunMode == config.RunModeStandard

	expiry := service.NewRechargeBonusExpiryService(rechargeBonusRepo, billingCache, 5*time.Minute)
	if runBG {
		expiry.Start()
	}

	supportTicketSvc := service.NewSupportTicketService(supportTicketRepo, settingService)
	billingPreviewSvc := service.NewBillingPreviewService(apiKeyService, channelService, billingService, modelPricingResolver)
	monitorV3Svc := service.NewChannelMonitorV3Service(channelMonitorV3Repo, groupRepo)
	monitorGroupProbe := service.NewMonitorGroupProbeService(groupRepo, gatewayService, openaiGatewayService, concurrencyService, accountTestService)
	monitorV3Prober := service.NewChannelMonitorV3Prober(channelMonitorV3Repo, monitorGroupProbe, settingService)
	if runBG {
		monitorV3Prober.Start()
	}

	candyRunner := channelMonitorV2Service.ConfigureCandy(monitorGroupProbe)
	if runBG && candyRunner != nil {
		candyRunner.Start()
	}

	intelScheduler := service.NewKittyIntelligenceScheduler(accountTestService)
	if runBG {
		intelScheduler.Start()
	}

	var perfSvc *service.AccountPerformanceService
	if sqlDB != nil {
		perfSvc = service.NewAccountPerformanceService(repository.NewAccountPerformanceRepository(sqlDB))
		perfSvc.SetOpsService(opsService)
		if adminAccountHandler != nil {
			adminAccountHandler.SetPerformanceService(perfSvc)
		}
		if runBG {
			perfSvc.Start()
		}
	}

	userCleanupSvc := service.NewUserCleanupService(sqlDB, authCacheInvalidator)

	qqbotSvc := service.ProvideQQBotService(
		sqlDB,
		secretEncryptor,
		cfg,
		authCacheInvalidator,
		billingCache,
		emailService,
		settingService,
		userRepo,
		channelMonitorV2Service,
		groupRepo,
		channelRepo,
		channelService,
		modelPricingResolver,
		billingService,
		userGroupRateRepo,
		apiKeyService,
		accountRepo,
		concurrencyService,
		balanceNotifyService,
	)

	return &KittyModules{
		SQLDB:                  sqlDB,
		EntClient:              entClient,
		RedisClient:            redisClient,
		Config:                 cfg,
		BillingExport:          admin.NewBillingExportHandler(service.NewBillingExportService(sqlDB)),
		RechargeBonus:          NewKittyRechargeBonusHandler(paymentService),
		AdminRechargeBonus:     admin.NewKittyRechargeBonusHandler(service.NewKittyRechargeBonusAdminStore(sqlDB)),
		RechargeBonusExpiry:    expiry,
		SupportTicket:          NewSupportTicketHandler(supportTicketSvc),
		BillingPreview:         NewBillingPreviewHandler(billingPreviewSvc),
		ChannelMonitorV3:       NewChannelMonitorV3Handler(monitorV3Svc, apiKeyService, settingService),
		ChannelMonitorV3Prober: monitorV3Prober,
		ChannelMonitorV2Candy:  candyRunner,
		IntelligenceScheduler:  intelScheduler,
		AccountPerformance:     perfSvc,
		UserCleanup:            admin.NewUserCleanupHandler(userCleanupSvc),
		QQBotService:           qqbotSvc,
		QQBot:                  NewQQBotHandler(qqbotSvc),
	}
}
