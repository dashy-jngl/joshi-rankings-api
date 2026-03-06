package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type visitor struct {
	tokens    float64
	lastSeen  time.Time
}

// RateLimit returns a simple token bucket rate limiter per IP.
// rate: tokens added per second, burst: max tokens.
func RateLimit(rate float64, burst int) gin.HandlerFunc {
	var mu sync.Mutex
	visitors := make(map[string]*visitor)

	// Cleanup old entries every 3 minutes
	go func() {
		for {
			time.Sleep(3 * time.Minute)
			mu.Lock()
			for ip, v := range visitors {
				if time.Since(v.lastSeen) > 5*time.Minute {
					delete(visitors, ip)
				}
			}
			mu.Unlock()
		}
	}()

	return func(c *gin.Context) {
		ip := c.ClientIP()
		mu.Lock()

		v, exists := visitors[ip]
		if !exists {
			v = &visitor{tokens: float64(burst), lastSeen: time.Now()}
			visitors[ip] = v
		}

		// Add tokens based on elapsed time
		elapsed := time.Since(v.lastSeen).Seconds()
		v.tokens += elapsed * rate
		if v.tokens > float64(burst) {
			v.tokens = float64(burst)
		}
		v.lastSeen = time.Now()

		if v.tokens < 1 {
			mu.Unlock()
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "Rate limit exceeded"})
			return
		}

		v.tokens--
		mu.Unlock()
		c.Next()
	}
}
