package middleware

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"joshi-rankings-api/handlers"
)

// APIKeyAuth requires a valid X-API-Key header.
func APIKeyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("X-API-Key")
		expectedApiKey := os.Getenv("API_KEY")

		if apiKey == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API key required"})
			return
		}

		if apiKey != expectedApiKey {
			log.Printf("API key auth failed from %s", c.ClientIP())
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		c.Next()
	}
}

// SessionAuth requires a valid session token (cookie or Bearer header).
func SessionAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		sess := handlers.LookupSession(token)
		if sess == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			return
		}
		c.Set("user_id", sess.UserID)
		c.Set("username", sess.Username)
		c.Set("role", sess.Role)
		c.Next()
	}
}

// OptionalAuth sets user context if a valid session exists, but doesn't abort.
func OptionalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		sess := handlers.LookupSession(token)
		if sess != nil {
			c.Set("user_id", sess.UserID)
			c.Set("username", sess.Username)
			c.Set("role", sess.Role)
		}
		c.Next()
	}
}

// RequireRole checks that the authenticated user has one of the allowed roles.
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Forbidden"})
			return
		}
		for _, r := range roles {
			if role.(string) == r {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions"})
	}
}

// APIKeyOrAdminSession accepts either a valid API key OR an admin session.
func APIKeyOrAdminSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Try API key first
		apiKey := c.GetHeader("X-API-Key")
		expected := os.Getenv("API_KEY")
		if apiKey != "" && apiKey == expected {
			c.Next()
			return
		}

		// Try session
		token := extractToken(c)
		sess := handlers.LookupSession(token)
		if sess != nil && sess.Role == "admin" {
			c.Set("user_id", sess.UserID)
			c.Set("username", sess.Username)
			c.Set("role", sess.Role)
			c.Next()
			return
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API key or admin session required"})
	}
}

func extractToken(c *gin.Context) string {
	token, _ := c.Cookie("joshi_session")
	if token != "" {
		return token
	}
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return auth[7:]
	}
	return ""
}
