package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"joshi-rankings-api/models"
)

// UserSession holds in-memory session data.
type UserSession struct {
	UserID    uint
	Username  string
	Role      string
	ExpiresAt time.Time
}

var (
	sessions   = map[string]*UserSession{}
	sessionsMu sync.RWMutex
	setupMu    sync.Mutex
	setupDone  bool
)

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// LookupSession finds a valid session by token. Exported for middleware use.
func LookupSession(token string) *UserSession {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	sess, ok := sessions[token]
	if !ok || time.Now().After(sess.ExpiresAt) {
		return nil
	}
	return sess
}

// Login handles POST /api/auth/login
func Login(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Username string `json:"username" binding:"required"`
			Password string `json:"password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Username and password required"})
			return
		}

		var user models.User
		if err := db.Where("username = ?", req.Username).First(&user).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
			return
		}

		token, err := generateToken()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create session"})
			return
		}

		now := time.Now()
		user.LastLoginAt = &now
		db.Save(&user)

		sessionsMu.Lock()
		sessions[token] = &UserSession{
			UserID:    user.ID,
			Username:  user.Username,
			Role:      user.Role,
			ExpiresAt: now.Add(24 * time.Hour),
		}
		sessionsMu.Unlock()

		secure := os.Getenv("GIN_MODE") == "release"
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie("joshi_session", token, 86400, "/", "", secure, true)
		c.JSON(http.StatusOK, gin.H{
			"token": token,
			"user":  user,
		})
	}
}

// Logout handles POST /api/auth/logout
func Logout() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, _ := c.Cookie("joshi_session")
		if token == "" {
			token = extractBearerToken(c)
		}
		if token != "" {
			sessionsMu.Lock()
			delete(sessions, token)
			sessionsMu.Unlock()
		}
		secure := os.Getenv("GIN_MODE") == "release"
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie("joshi_session", "", -1, "/", "", secure, true)
		c.JSON(http.StatusOK, gin.H{"message": "Logged out"})
	}
}

// Me handles GET /api/auth/me
func Me() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, _ := c.Cookie("joshi_session")
		if token == "" {
			token = extractBearerToken(c)
		}
		sess := LookupSession(token)
		if sess == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Not authenticated"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"user_id":  sess.UserID,
			"username": sess.Username,
			"role":     sess.Role,
		})
	}
}

// Setup handles POST /api/auth/setup — bootstrap first admin account
func Setup(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		setupMu.Lock()
		defer setupMu.Unlock()

		if setupDone {
			c.JSON(http.StatusForbidden, gin.H{"error": "Setup already completed"})
			return
		}

		var count int64
		db.Model(&models.User{}).Count(&count)
		if count > 0 {
			setupDone = true
			c.JSON(http.StatusForbidden, gin.H{"error": "Setup already completed"})
			return
		}

		var req struct {
			Username string `json:"username" binding:"required"`
			Password string `json:"password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Username and password required"})
			return
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
			return
		}

		user := models.User{
			Username:     req.Username,
			PasswordHash: string(hash),
			DisplayName:  req.Username,
			Role:         "admin",
		}
		if err := db.Create(&user).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
			return
		}

		setupDone = true
		c.JSON(http.StatusCreated, gin.H{"user": user})
	}
}

func extractBearerToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if len(auth) > 7 && auth[:7] == "Bearer " {
		return auth[7:]
	}
	return ""
}
