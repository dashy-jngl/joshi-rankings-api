// fetch_twitter_pfps queries the local SQLite DB directly for wrestlers
// without profile pics who have a Twitter/X link, scrapes their profile
// image via Twitter's syndication API, downloads it, and updates the DB.
//
// Run from the joshi-rankings-api root (so static/ paths resolve):
//   go run scripts/fetch_twitter_pfps.go [-db joshi.db] [-dry-run] [-limit 50]

package main

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var (
	dbPath = flag.String("db", "", "Path to joshi.db (default: $DB_PATH or joshi.db)")
	dryRun = flag.Bool("dry-run", false, "Print what would be done without making changes")
	limit  = flag.Int("limit", 0, "Max wrestlers to process (0 = all)")
)

type candidate struct {
	ID         int
	Name       string
	ScreenName string
}

var screenNameRe = regexp.MustCompile(`(?:twitter\.com|x\.com)/([A-Za-z0-9_]+)`)

func extractScreenName(url string) string {
	m := screenNameRe.FindStringSubmatch(url)
	if m == nil {
		return ""
	}
	name := m[1]
	lower := strings.ToLower(name)
	if lower == "intent" || lower == "i" || lower == "search" || lower == "hashtag" || lower == "home" {
		return ""
	}
	return name
}

func fetchProfileImageURL(screenName string) (string, error) {
	url := fmt.Sprintf("https://api.fxtwitter.com/%s", screenName)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		return "", fmt.Errorf("account closed/suspended (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	re := regexp.MustCompile(`"avatar_url":"([^"]+)"`)
	match := re.FindStringSubmatch(string(body))
	if match == nil {
		return "", fmt.Errorf("no avatar found (account may be closed)")
	}

	imageURL := strings.Replace(match[1], "_normal.", "_400x400.", 1)
	return imageURL, nil
}

func downloadImage(imageURL string, wrestlerID int) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download HTTP %d", resp.StatusCode)
	}

	ext := ".jpg"
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "png") {
		ext = ".png"
	} else if strings.Contains(ct, "webp") {
		ext = ".webp"
	}

	filename := fmt.Sprintf("%d%s", wrestlerID, ext)
	diskPath := filepath.Join("static", "images", "wrestlers", filename)

	if err := os.MkdirAll(filepath.Dir(diskPath), 0755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	f, err := os.Create(diskPath)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return "/static/images/wrestlers/" + filename, nil
}

func main() {
	flag.Parse()

	db := *dbPath
	if db == "" {
		db = os.Getenv("DB_PATH")
	}
	if db == "" {
		db = "joshi.db"
	}

	conn, err := sql.Open("sqlite3", db+"?_journal_mode=WAL&_busy_timeout=10000&mode=rwc")
	if err != nil {
		log.Fatalf("Failed to open DB: %v", err)
	}
	defer conn.Close()

	// Find wrestlers without images who have a Twitter link
	query := `
		SELECT w.id, w.name, s.url
		FROM wrestlers w
		JOIN socials s ON s.wrestler_id = w.id AND s.name = 'twitter'
		WHERE (w.image_url IS NULL OR w.image_url = '')
		  AND (w.image_local IS NULL OR w.image_local = '')
		ORDER BY w.id
	`
	if *limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", *limit)
	}

	rows, err := conn.Query(query)
	if err != nil {
		log.Fatalf("Query failed: %v", err)
	}
	defer rows.Close()

	var candidates []candidate
	for rows.Next() {
		var id int
		var name, twitterURL string
		if err := rows.Scan(&id, &name, &twitterURL); err != nil {
			log.Printf("Scan error: %v", err)
			continue
		}
		sn := extractScreenName(twitterURL)
		if sn != "" {
			candidates = append(candidates, candidate{id, name, sn})
		}
	}

	log.Printf("Found %d wrestlers without images who have Twitter links", len(candidates))
	if len(candidates) == 0 {
		log.Println("Nothing to do!")
		return
	}

	var success, failed, closed int

	for i, c := range candidates {
		log.Printf("[%d/%d] %s (@%s)...", i+1, len(candidates), c.Name, c.ScreenName)

		imageURL, err := fetchProfileImageURL(c.ScreenName)
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "closed") || strings.Contains(errStr, "suspended") || strings.Contains(errStr, "HTTP 4") {
				log.Printf("  ⚠ Closed/suspended: @%s", c.ScreenName)
				closed++
			} else {
				log.Printf("  ✗ Error: %v", err)
				failed++
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if *dryRun {
			log.Printf("  ✓ [DRY RUN] Would set: %s", imageURL)
			success++
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// Download the image locally
		localPath, err := downloadImage(imageURL, c.ID)
		if err != nil {
			log.Printf("  ✗ Download failed: %v", err)
			failed++
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// Update DB directly
		_, err = conn.Exec(
			`UPDATE wrestlers SET image_url = ?, image_local = ? WHERE id = ?`,
			imageURL, localPath, c.ID,
		)
		if err != nil {
			log.Printf("  ✗ DB update failed: %v", err)
			failed++
			continue
		}

		log.Printf("  ✓ Saved %s → %s", c.ScreenName, localPath)
		success++

		// Be nice to Twitter
		time.Sleep(1 * time.Second)
	}

	log.Println()
	log.Println("=== Results ===")
	log.Printf("Success: %d", success)
	log.Printf("Closed/suspended: %d", closed)
	log.Printf("Failed: %d", failed)
	log.Printf("Total processed: %d", success+closed+failed)
}
