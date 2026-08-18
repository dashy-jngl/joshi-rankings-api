package main

import (
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// handleBackground picks a random page backdrop from static/backgrounds —
// on the shared box that's a symlink into the joshi.fyi (wwr2) asset
// library, so both sites draw from one wallpaper pool. The directory is
// rescanned per call, so new drops need no restart. When the wwr2 optimize
// script has built .web/ variants (large/mobile + tiny blur-up placeholder)
// they're served instead of the full-fidelity originals.
func handleBackground() gin.HandlerFunc {
	return func(c *gin.Context) {
		dir := filepath.Join("static", "backgrounds")
		entries, err := os.ReadDir(dir)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{})
			return
		}
		var names []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			switch strings.ToLower(filepath.Ext(e.Name())) {
			case ".jpg", ".jpeg", ".png", ".webp", ".avif":
				names = append(names, e.Name())
			}
		}
		if len(names) == 0 {
			c.JSON(http.StatusOK, gin.H{})
			return
		}
		pick := names[rand.Intn(len(names))]
		base := strings.TrimSuffix(pick, filepath.Ext(pick))
		large := "/static/backgrounds/" + pick
		if _, err := os.Stat(filepath.Join(dir, ".web", base+".webp")); err == nil {
			large = "/static/backgrounds/.web/" + base + ".webp"
		}
		mobile := large
		if _, err := os.Stat(filepath.Join(dir, ".web", base+"_m.webp")); err == nil {
			mobile = "/static/backgrounds/.web/" + base + "_m.webp"
		}
		placeholder := ""
		if _, err := os.Stat(filepath.Join(dir, ".web", base+"_p.webp")); err == nil {
			placeholder = "/static/backgrounds/.web/" + base + "_p.webp"
		}
		c.JSON(http.StatusOK, gin.H{
			"large":       large,
			"mobile":      mobile,
			"placeholder": placeholder,
		})
	}
}
