package middleware

import (
	"os"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func CORS() gin.HandlerFunc {
	origins := []string{"http://localhost:8080"}
	if env := os.Getenv("ALLOWED_ORIGINS"); env != "" {
		origins = strings.Split(env, ",")
		for i := range origins {
			origins[i] = strings.TrimSpace(origins[i])
		}
	}

	return cors.New(cors.Config{
		AllowOrigins:     origins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Cookie", "Authorization", "X-API-Key"},
		AllowCredentials: true,
	})
}
