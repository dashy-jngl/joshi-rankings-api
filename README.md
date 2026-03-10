# 🏆 Joshi Power Rankings API

A REST API that tracks joshi (women's) pro wrestling match results and calculates dynamic power rankings using an ELO rating system.

Built with **Go** as a backend portfolio piece.

![Go](https://img.shields.io/badge/Go-00ADD8?style=flat&logo=go&logoColor=white)
![SQLite](https://img.shields.io/badge/SQLite-003B57?style=flat&logo=sqlite&logoColor=white)
![License](https://img.shields.io/badge/License-MIT-green)

## Features

- 🤼‍♀️ **Wrestler database** with promotion tracking
- 📊 **ELO rating system** with title match bonuses
- 🕷️ **Auto-scraper** fetches results from official promotion sites (Stardom, TJPW, Marvelous)
- ⚡ **Concurrent scraping** using goroutines + fan-out pattern
- 🔍 **Fuzzy name matching** with alias support for Japanese/English names
- 📈 **Power rankings** — global or per-promotion

## Tech Stack

| What | Why |
|------|-----|
| [Go](https://go.dev) | Fast, typed, great for APIs and concurrency |
| [Gin](https://gin-gonic.com) | Most popular Go web framework |
| [GORM](https://gorm.io) | Go ORM with SQLite + Postgres support |
| [colly](http://go-colly.org) | Elegant Go scraping framework |
| [SQLite](https://sqlite.org) | Zero-config embedded database |

## Quick Start

```bash
# Clone
git clone https://github.com/yourusername/joshi-rankings-api.git
cd joshi-rankings-api

# Install dependencies
go mod download

# Run (auto-creates DB + seeds wrestlers)
make run

# Or with hot reload
make dev
```

The API starts at `http://localhost:8080`.

## API Endpoints

### Wrestlers
```bash
# List all wrestlers
curl http://localhost:8080/api/wrestlers

# Filter by promotion
curl http://localhost:8080/api/wrestlers?promotion=stardom

# Get one wrestler
curl http://localhost:8080/api/wrestlers/1

# Add a wrestler
curl -X POST http://localhost:8080/api/wrestlers \
  -H "Content-Type: application/json" \
  -d '{"name": "Sareee", "promotion": "freelance"}'
```

### Matches
```bash
# Log a match result (triggers ELO recalc)
curl -X POST http://localhost:8080/api/matches \
  -H "Content-Type: application/json" \
  -d '{
    "winner_id": 6,
    "loser_id": 4,
    "match_type": "singles",
    "event_name": "Stardom New Year Stars 2026",
    "date": "2026-01-03T00:00:00Z",
    "is_title_match": true
  }'

# List matches (optional wrestler filter)
curl http://localhost:8080/api/matches?wrestler_id=6
```

### Rankings
```bash
# Global power rankings
curl http://localhost:8080/api/rankings

# Top 10
curl http://localhost:8080/api/rankings?limit=10

# By promotion
curl http://localhost:8080/api/rankings?promotion=stardom
```

### Scraper
```bash
# Check scraper status
curl http://localhost:8080/api/scraper/status

# Trigger manual scrape
curl -X POST http://localhost:8080/api/scraper/run

# View scrape log
curl http://localhost:8080/api/scraper/log
```

### Stats (stretch)
```bash
# ELO history for a wrestler
curl http://localhost:8080/api/wrestlers/6/history

# Head-to-head
curl "http://localhost:8080/api/head-to-head?w1=6&w2=4"
```

## ELO System

| Parameter | Value |
|-----------|-------|
| Starting ELO | 1200 |
| K-factor | 32 |
| Title match multiplier | 1.5× |

Upsets are naturally rewarded — beating a higher-rated wrestler gives a bigger ELO boost.

```
Example: HATE (1400) beats Tam Nakano (1500)
→ Expected outcome favoured Tam → HATE gets a big boost
→ HATE: 1400 → ~1420
→ Tam:  1500 → ~1480
```

## Project Structure

```
joshi-rankings-api/
├── main.go                 # Entry point, router, scraper startup
├── handlers/               # HTTP handlers (wrestlers, matches, rankings)
├── models/                 # Data models + DB methods
├── services/               # ELO calculation, ranking generation
├── scraper/                # Auto-scraper system (one file per promotion)
├── database/               # DB connection + migrations
├── middleware/              # CORS etc.
├── seeds/                  # Seed data (wrestler JSON)
└── docs/                   # API documentation
```

## Design Decisions

- **Interfaces everywhere** — `WrestlerStore` and `RankingCalculator` interfaces make it easy to swap SQLite for Postgres, or ELO for Glicko-2
- **Concurrent scrapers** — each promotion is scraped in its own goroutine using the fan-out pattern
- **Respectful scraping** — 3-second rate limit, proper User-Agent, official sources only
- **Alias table** — handles Japanese ↔ English name variants for fuzzy matching

## License

MIT

---

*Built by Dashy 🇦🇺 — because joshi wrestling deserves better stats*
