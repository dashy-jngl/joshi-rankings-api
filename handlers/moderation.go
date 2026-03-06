package handlers

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"joshi-rankings-api/models"
)

// SetWrestlerImage handles PUT /api/mod/wrestlers/:id/image
// Allows admin/moderator to set a wrestler's profile picture URL.
func SetWrestlerImage(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid wrestler ID"})
			return
		}

		var wrestler models.Wrestler
		if err := db.First(&wrestler, id).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Wrestler not found"})
			return
		}

		var req struct {
			ImageURL string `json:"image_url" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "image_url required"})
			return
		}

		// Validate URL scheme
		if !strings.HasPrefix(req.ImageURL, "http://") && !strings.HasPrefix(req.ImageURL, "https://") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Only http and https URLs are allowed"})
			return
		}

		// Block private/loopback IPs
		parsedURL, err := url.Parse(req.ImageURL)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid URL"})
			return
		}
		host := parsedURL.Hostname()
		if ip := net.ParseIP(host); ip != nil {
			if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				c.JSON(http.StatusBadRequest, gin.H{"error": "URLs pointing to private/internal addresses are not allowed"})
				return
			}
		} else if host == "localhost" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "URLs pointing to private/internal addresses are not allowed"})
			return
		}

		// Download image locally
		localPath := ""
		func() {
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Get(req.ImageURL)
			if err != nil {
				log.Printf("[mod] Warning: could not download image for %s: %v", wrestler.Name, err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				log.Printf("[mod] Warning: image download returned %d for %s", resp.StatusCode, wrestler.Name)
				return
			}

			// Determine extension from URL or content-type
			ext := ".jpg"
			if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "png") {
				ext = ".png"
			} else if strings.Contains(ct, "webp") {
				ext = ".webp"
			} else if strings.Contains(ct, "gif") {
				ext = ".gif"
			} else if urlExt := filepath.Ext(strings.SplitN(req.ImageURL, "?", 2)[0]); urlExt == ".png" || urlExt == ".webp" || urlExt == ".gif" || urlExt == ".jpg" || urlExt == ".jpeg" {
				ext = urlExt
			}

			filename := fmt.Sprintf("%d%s", wrestler.ID, ext)
			diskPath := filepath.Join("static", "images", "wrestlers", filename)
			f, err := os.Create(diskPath)
			if err != nil {
				log.Printf("[mod] Warning: could not create file %s: %v", diskPath, err)
				return
			}
			defer f.Close()
			if _, err := io.Copy(f, resp.Body); err != nil {
				log.Printf("[mod] Warning: could not write image file: %v", err)
				return
			}
			localPath = "/static/images/wrestlers/" + filename
		}()

		updates := map[string]interface{}{"image_url": req.ImageURL}
		if localPath != "" {
			updates["image_local"] = localPath
		}
		db.Model(&wrestler).Updates(updates)

		username, _ := c.Get("username")
		log.Printf("[mod] %s set image for %s (#%d): %s (local: %s)", username, wrestler.Name, wrestler.ID, req.ImageURL, localPath)

		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Image updated for %s", wrestler.Name)})
	}
}

// GhostWrestler handles POST /api/mod/wrestlers/:id/ghost
// Converts a tracked wrestler into a ghost:
// 1. Adds their CagematchID to skipped_wrestlers
// 2. Converts all their match_participants to ghost entries (wrestler_id=0)
// 3. Deletes the wrestler record and related data
func GhostWrestler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid wrestler ID"})
			return
		}

		var wrestler models.Wrestler
		if err := db.First(&wrestler, id).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Wrestler not found"})
			return
		}

		username, _ := c.Get("username")
		log.Printf("[mod] %s is ghosting wrestler %s (#%d, CM#%d)", username, wrestler.Name, wrestler.ID, wrestler.CagematchID)

		// Start transaction
		tx := db.Begin()

		// 1. Add to skipped_wrestlers if they have a CagematchID
		if wrestler.CagematchID > 0 {
			tx.Where("cagematch_id = ?", wrestler.CagematchID).
				Assign(models.SkippedWrestler{CagematchID: int(wrestler.CagematchID), Name: wrestler.Name}).
				FirstOrCreate(&models.SkippedWrestler{})
		}

		// 2. Convert match_participants: set wrestler_id=0, fill ghost fields
		var count int64
		tx.Model(&models.MatchParticipant{}).Where("wrestler_id = ?", id).Count(&count)

		tx.Model(&models.MatchParticipant{}).Where("wrestler_id = ?", id).Updates(map[string]interface{}{
			"wrestler_id":        0,
			"ghost_name":         wrestler.Name,
			"ghost_cagematch_id": int(wrestler.CagematchID),
		})

		// 3. Delete related data
		tx.Where("wrestler_id = ?", id).Delete(&models.ELOHistory{})
		tx.Where("wrestler_id = ?", id).Delete(&models.MomentumHistory{})
		tx.Where("wrestler_id = ?", id).Delete(&models.PromotionHistory{})
		tx.Where("wrestler_id = ?", id).Delete(&models.TitleReign{})
		tx.Where("wrestler_id = ?", id).Delete(&models.WrestlerAlias{})
		tx.Where("wrestler_id = ?", id).Delete(&models.Socials{})

		// 4. Delete the wrestler
		tx.Delete(&models.Wrestler{}, id)

		if err := tx.Commit().Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to ghost wrestler"})
			return
		}

		log.Printf("[mod] Ghosted %s — %d match participations converted to ghost", wrestler.Name, count)

		c.JSON(http.StatusOK, gin.H{
			"message":              fmt.Sprintf("👻 %s has been ghosted", wrestler.Name),
			"participations_moved": count,
		})
	}
}
