package middleware

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// CSRFProtection checks Origin/Referer headers on state-changing requests
// to prevent cross-site request forgery when using cookie-based auth.
func CSRFProtection() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only check state-changing methods
		method := c.Request.Method
		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			c.Next()
			return
		}

		// If using API key auth (not cookie), skip CSRF check
		if c.GetHeader("X-API-Key") != "" {
			c.Next()
			return
		}

		// If using Bearer token (not cookie), skip CSRF check
		if strings.HasPrefix(c.GetHeader("Authorization"), "Bearer ") {
			c.Next()
			return
		}

		// For cookie-based auth, validate Origin or Referer
		origin := c.GetHeader("Origin")
		referer := c.GetHeader("Referer")

		if origin == "" && referer == "" {
			// No origin info — block to be safe
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Missing Origin header"})
			return
		}

		allowed := getAllowedOrigins()
		checkURL := origin
		if checkURL == "" {
			checkURL = referer
		}

		parsed, err := url.Parse(checkURL)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Invalid origin"})
			return
		}
		requestOrigin := parsed.Scheme + "://" + parsed.Host

		for _, a := range allowed {
			if requestOrigin == a {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Cross-origin request blocked"})
	}
}

func getAllowedOrigins() []string {
	origins := []string{"http://localhost:8080"}
	if env := os.Getenv("ALLOWED_ORIGINS"); env != "" {
		origins = strings.Split(env, ",")
		for i := range origins {
			origins[i] = strings.TrimSpace(origins[i])
		}
	}
	return origins
}
