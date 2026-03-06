package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"joshi-rankings-api/models"
)

func ListUsers(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var users []models.User
		db.Order("id asc").Find(&users)
		c.JSON(http.StatusOK, users)
	}
}

func CreateUser(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Username    string `json:"username" binding:"required"`
			Password    string `json:"password" binding:"required"`
			DisplayName string `json:"display_name"`
			Role        string `json:"role"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Username and password required"})
			return
		}
		if req.Role == "" {
			req.Role = "user"
		}
		if req.Role != "admin" && req.Role != "moderator" && req.Role != "user" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid role"})
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
			DisplayName:  req.DisplayName,
			Role:         req.Role,
		}
		if user.DisplayName == "" {
			user.DisplayName = req.Username
		}
		if err := db.Create(&user).Error; err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "Username already exists"})
			return
		}
		c.JSON(http.StatusCreated, user)
	}
}

func UpdateUser(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
			return
		}

		var user models.User
		if err := db.First(&user, id).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}

		var req struct {
			DisplayName *string `json:"display_name"`
			Role        *string `json:"role"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
			return
		}

		currentUserID, _ := c.Get("user_id")
		if req.Role != nil && *req.Role != user.Role && user.ID == currentUserID.(uint) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Cannot change your own role"})
			return
		}

		if req.Role != nil {
			if *req.Role != "admin" && *req.Role != "moderator" && *req.Role != "user" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid role"})
				return
			}
			user.Role = *req.Role
		}
		if req.DisplayName != nil {
			user.DisplayName = *req.DisplayName
		}

		db.Save(&user)
		c.JSON(http.StatusOK, user)
	}
}

func DeleteUser(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
			return
		}

		currentUserID, _ := c.Get("user_id")
		if uint(id) == currentUserID.(uint) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Cannot delete yourself"})
			return
		}

		var user models.User
		if err := db.First(&user, id).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}

		db.Delete(&user)
		c.JSON(http.StatusOK, gin.H{"message": "User deleted"})
	}
}

func ResetUserPassword(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
			return
		}

		var user models.User
		if err := db.First(&user, id).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}

		var req struct {
			Password string `json:"password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Password required"})
			return
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
			return
		}

		user.PasswordHash = string(hash)
		db.Save(&user)
		c.JSON(http.StatusOK, gin.H{"message": "Password reset successfully"})
	}
}
