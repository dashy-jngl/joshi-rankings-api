package main

import (
	"fmt"
	"log"
	"joshi-rankings-api/database"
	"joshi-rankings-api/scraper"
)

func main() {
	db, _ := database.InitDB("joshi.db")
	cm := scraper.NewCagematchScraper(db)

	fmt.Println("Scraping Suzu Suzuki (CM#20600)...")
	matches, err := cm.TestScrapeWrestler(20600)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Scraped %d raw matches from Cagematch\n", len(matches))

	// Check which ones are missing from DB
	proc := scraper.NewProcessor(db, cm)
	newCount := proc.CollectMatches(matches)
	fmt.Printf("New matches stored: %d\n", newCount)

	var count int64
	db.Table("match_participants").Where("wrestler_id = 1").Count(&count)
	fmt.Printf("Suzu total matches in DB: %d\n", count)
}
