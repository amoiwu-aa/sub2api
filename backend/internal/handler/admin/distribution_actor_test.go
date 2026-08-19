package admin

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type distributionManagedByStub struct {
	service.AdminService
	ok       bool
	err      error
	targetID int64
	adminID  int64
}

func (s *distributionManagedByStub) UserIsManagedBy(_ context.Context, targetUserID, adminID int64) (bool, error) {
	s.targetID = targetUserID
	s.adminID = adminID
	return s.ok, s.err
}

func distributionActorContext(w *httptest.ResponseRecorder) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
	c.Set(string(middleware.ContextKeyUserRole), service.RoleAffiliateAdmin)
	return c
}

func TestDistributionActor_requireManagedUser_AllowsManaged(t *testing.T) {
	stub := &distributionManagedByStub{ok: true}
	w := httptest.NewRecorder()
	c := distributionActorContext(w)

	require.True(t, requireManagedUser(c, stub, 11))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(11), stub.targetID)
	require.Equal(t, int64(7), stub.adminID)
}

func TestDistributionActor_requireManagedUser_HidesUnmanagedAsNotFound(t *testing.T) {
	stub := &distributionManagedByStub{ok: false}
	w := httptest.NewRecorder()
	c := distributionActorContext(w)

	require.False(t, requireManagedUser(c, stub, 99))
	require.Equal(t, http.StatusNotFound, w.Code)

	var envelope struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Equal(t, "MANAGED_USER_NOT_FOUND", envelope.Reason)
}

func TestDistributionActor_requireManagedUser_LookupErrorIsNotFound(t *testing.T) {
	stub := &distributionManagedByStub{err: errors.New("store down")}
	w := httptest.NewRecorder()
	c := distributionActorContext(w)

	require.False(t, requireManagedUser(c, stub, 11))
	require.Equal(t, http.StatusNotFound, w.Code)

	var envelope struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Equal(t, "MANAGED_USER_NOT_FOUND", envelope.Reason)
}

func TestDistributionActor_parsePositiveAmount(t *testing.T) {
	require.NoError(t, parsePositiveAmount(1.25))
	require.Error(t, parsePositiveAmount(0))
	require.Error(t, parsePositiveAmount(-3))
	require.Error(t, parsePositiveAmount(math.NaN()))
	require.Error(t, parsePositiveAmount(math.Inf(1)))
	require.Error(t, parsePositiveAmount(math.Inf(-1)))
}
