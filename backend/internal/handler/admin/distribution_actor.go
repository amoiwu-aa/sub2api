package admin

import (
	"errors"
	"math"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func requireManagedUser(c *gin.Context, adminService service.AdminService, targetUserID int64) bool {
	if adminService == nil {
		response.ErrorWithDetails(c, http.StatusNotFound, "managed user not found", "MANAGED_USER_NOT_FOUND", nil)
		return false
	}
	ok, err := adminService.UserIsManagedBy(c.Request.Context(), targetUserID, getAdminIDFromContext(c))
	if err != nil || !ok {
		// Always 404 so unmanaged vs missing users are not distinguishable.
		response.ErrorWithDetails(c, http.StatusNotFound, "managed user not found", "MANAGED_USER_NOT_FOUND", nil)
		return false
	}
	return true
}

func parsePositiveAmount(raw float64) error {
	if math.IsNaN(raw) || math.IsInf(raw, 0) || raw <= 0 {
		return errors.New("amount must be a positive finite number")
	}
	return nil
}
