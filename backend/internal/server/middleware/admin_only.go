package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AdminOnly 管理员权限中间件
// 必须在JWTAuth中间件之后使用
func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, ok := GetUserRoleFromContext(c)
		if !ok {
			AbortWithError(c, 401, "UNAUTHORIZED", "User not found in context")
			return
		}

		// 检查是否为管理员
		if role != service.RoleAdmin {
			AbortWithError(c, 403, "FORBIDDEN", "Admin access required")
			return
		}

		c.Next()
	}
}

// RequireSuperAdmin 只放行总管理员。分销管理员可以进 /admin，但不能碰全站接口。
func RequireSuperAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, ok := GetUserRoleFromContext(c)
		if !ok {
			AbortWithError(c, 401, "UNAUTHORIZED", "User not found in context")
			return
		}
		if role != service.RoleAdmin {
			AbortWithError(c, 403, "FORBIDDEN", "Super admin access required")
			return
		}
		c.Next()
	}
}

// RequireAffiliateAdmin 只放行分销管理员。总管理员继续走全站接口，不能误入分销专属 API。
func RequireAffiliateAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, ok := GetUserRoleFromContext(c)
		if !ok {
			AbortWithError(c, 401, "UNAUTHORIZED", "User not found in context")
			return
		}
		if role != service.RoleAffiliateAdmin {
			AbortWithError(c, 403, "FORBIDDEN", "Affiliate admin access required")
			return
		}
		c.Next()
	}
}
