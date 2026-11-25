package admin

import (
	"errors"
	"sort"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
)

// ListPlatforms returns global account platform counts without list filters.
// GET /api/v1/admin/accounts/platforms
func (h *AccountHandler) ListPlatforms(c *gin.Context) {
	counts, err := h.adminService.ListAccountPlatformCounts(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if counts == nil {
		counts = []service.AccountPlatformCount{}
	}
	response.Success(c, counts)
}

func (h *AccountHandler) BatchSyncUpstreamModels(c *gin.Context) {
	var req struct {
		AccountIDs []int64 `json:"account_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	accountIDs := normalizeInt64IDList(req.AccountIDs)
	if len(accountIDs) == 0 {
		response.BadRequest(c, "account_ids is required")
		return
	}
	if h.accountTestService == nil {
		response.InternalError(c, "Account test service is not configured")
		return
	}

	accounts, err := h.adminService.GetAccountsByIDs(c.Request.Context(), accountIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	accountsByID := make(map[int64]*service.Account, len(accounts))
	for _, account := range accounts {
		if account != nil {
			accountsByID[account.ID] = account
		}
	}

	type syncError struct {
		AccountID int64  `json:"account_id"`
		Error     string `json:"error"`
	}
	type syncWarning struct {
		AccountID int64  `json:"account_id"`
		Code      string `json:"code"`
		Message   string `json:"message"`
	}

	successIDs := make([]int64, 0, len(accountIDs))
	failedIDs := make([]int64, 0)
	errorsByAccount := make([]syncError, 0)
	warnings := make([]syncWarning, 0)
	for _, accountID := range accountIDs {
		if accountsByID[accountID] == nil {
			failedIDs = append(failedIDs, accountID)
			errorsByAccount = append(errorsByAccount, syncError{AccountID: accountID, Error: "account not found"})
		}
	}
	const maxConcurrency = 5
	g, gctx := errgroup.WithContext(c.Request.Context())
	g.SetLimit(maxConcurrency)
	var mu sync.Mutex

	for _, accountID := range accountIDs {
		accountID := accountID
		account := accountsByID[accountID]
		if account == nil {
			continue
		}

		g.Go(func() error {
			catalog, syncErr := h.accountTestService.ReplaceModelMappingWithUpstream(gctx, account)
			mu.Lock()
			defer mu.Unlock()
			if syncErr != nil {
				failedIDs = append(failedIDs, accountID)
				errorsByAccount = append(errorsByAccount, syncError{
					AccountID: accountID,
					Error:     safeUpstreamModelSyncErrorMessage(syncErr),
				})
				return nil
			}

			successIDs = append(successIDs, accountID)
			for _, warning := range catalog.Warnings {
				warnings = append(warnings, syncWarning{
					AccountID: accountID,
					Code:      warning.Code,
					Message:   warning.Message,
				})
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	sort.Slice(successIDs, func(i, j int) bool { return successIDs[i] < successIDs[j] })
	sort.Slice(failedIDs, func(i, j int) bool { return failedIDs[i] < failedIDs[j] })
	sort.Slice(errorsByAccount, func(i, j int) bool { return errorsByAccount[i].AccountID < errorsByAccount[j].AccountID })
	sort.Slice(warnings, func(i, j int) bool {
		if warnings[i].AccountID != warnings[j].AccountID {
			return warnings[i].AccountID < warnings[j].AccountID
		}
		return warnings[i].Code < warnings[j].Code
	})

	response.Success(c, gin.H{
		"total":       len(accountIDs),
		"success":     len(successIDs),
		"failed":      len(failedIDs),
		"success_ids": successIDs,
		"failed_ids":  failedIDs,
		"errors":      errorsByAccount,
		"warnings":    warnings,
	})
}

func safeUpstreamModelSyncErrorMessage(err error) string {
	var syncErr *service.UpstreamModelSyncError
	if errors.As(err, &syncErr) {
		return syncErr.SafeMessage()
	}
	return "Failed to sync upstream models from upstream"
}

