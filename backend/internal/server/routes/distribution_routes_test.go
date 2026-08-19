package routes

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDistributionAdminRoutesAreRegisteredBeforeSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{
		Distribution: adminhandler.NewDistributionHandler(nil, nil, nil, nil, nil, nil, nil),
	}}
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) { c.Next() })
	auditLog := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, auditLog, stepUp, nil, nil)

	found := map[string]bool{}
	for _, route := range router.Routes() {
		switch {
		case route.Method == http.MethodGet && route.Path == "/api/v1/admin/distribution/dashboard/snapshot":
			found["snapshot"] = true
		case route.Method == http.MethodGet && route.Path == "/api/v1/admin/distribution/usage/trend":
			found["trend"] = true
		case route.Method == http.MethodGet && route.Path == "/api/v1/admin/distribution/groups":
			found["groups"] = true
		case route.Method == http.MethodPost && route.Path == "/api/v1/admin/distribution/users/:id/balance-transfers":
			found["transfer"] = true
		case route.Method == http.MethodGet && route.Path == "/api/v1/admin/distribution/invites/profile":
			found["invites"] = true
		case route.Method == http.MethodGet && route.Path == "/api/v1/admin/distribution/users/:id/subscriptions":
			found["subscriptions"] = true
		case route.Method == http.MethodGet && route.Path == "/api/v1/admin/distribution/users/ranking":
			found["ranking"] = true
		case route.Method == http.MethodGet && route.Path == "/api/v1/admin/distribution/permissions":
			found["permissions"] = true
		}
	}
	require.True(t, found["snapshot"], "dashboard snapshot route missing")
	require.True(t, found["trend"], "usage trend route missing")
	require.True(t, found["groups"], "groups route missing")
	require.True(t, found["transfer"], "balance transfer route missing")
	require.True(t, found["invites"], "invite profile route missing")
	require.True(t, found["subscriptions"], "subscriptions route missing")
	require.True(t, found["ranking"], "user ranking route missing")
	require.True(t, found["permissions"], "distribution permissions route missing")
}
