# 🏆 Joshitori — Power Rankings for Joshi Pro Wrestling

**[joshitori.com](https://joshitori.com)** · *hoshitori for joshi*

A full-stack ranking system for women's professional wrestling, tracking **3,800+ wrestlers** across **250k+ matches** with dynamic ELO ratings, automated web crawling, and a real-time admin dashboard.

![Go](https://img.shields.io/badge/Go-00ADD8?style=flat&logo=go&logoColor=white)
![SQLite](https://img.shields.io/badge/SQLite-003B57?style=flat&logo=sqlite&logoColor=white)
![Hetzner](https://img.shields.io/badge/Hetzner-D50C2D?style=flat&logo=hetzner&logoColor=white)
![License](https://img.shields.io/badge/License-MIT-green)

## What It Does

Joshitori crawls match results from [Cagematch](https://www.cagematch.net), processes them through a custom ELO system, and produces ranked power ratings for every active women's wrestler — from WWE to small indie promotions in Japan.

### Key Features

- **ELO Rating System** — asymmetric K-factor, dynamic adjustments for title matches, draws, and multi-person bouts. Percentile-based tiers from 🌱 Seedling to 👑 Jotei
- **Automated Crawler** — scheduled Cagematch scraping with rate limiting, dedup via composite match keys, and graceful error handling
- **Profile Enrichment** — scrapes wrestler bios (birthdays, height/weight, socials, signature moves) with partial date support
- **PFP Fetcher** — bulk-fetches Twitter profile pictures via API for wrestlers without images
- **Full Admin Panel** — scraper controls, user management, test scraping, match validation, live activity log
- **REST API** — 40+ endpoints covering wrestlers, matches, rankings, ELO history, title reigns, promotions, head-to-head, network graphs, and match predictions
- **Session Auth + CSRF Protection** — bcrypt passwords, secure cookies, origin validation
- **Static Frontend** — responsive dark-theme UI with interactive charts (Chart.js), search, wrestler profiles, compare tool, and timeline explorer

## Architecture

```
┌─────────────┐     ┌──────────────┐     ┌──────────┐
│  Cagematch   │────▶│   Crawler    │────▶│  SQLite  │
│  (source)    │     │  (Go, cron)  │     │  (WAL)   │
└─────────────┘     └──────────────┘     └────┬─────┘
                                              │
┌─────────────┐     ┌──────────────┐          │
│   Browser    │────▶│   Gin API    │◀─────────┘
│              │◀────│  + Static    │
└─────────────┘     └──────────────┘
```

Single binary deployment. SQLite with WAL mode handles concurrent reads/writes from the API and crawler simultaneously.

## ELO System

| Parameter | Detail |
|-----------|--------|
| Base K-factor | 32, scaled by match importance |
| Loss penalty | 0.5× (asymmetric — losses hurt less) |
| Draw handling | Capped penalty, never worse than a loss |
| Title multiplier | Boosted K for championship matches |
| Tiers | Percentile-based: Jotei → Ace → Senshi → Estrella → Young Lioness → Seedling |

## Match Deduplication

Matches are deduplicated using deterministic composite keys: `date|event|match_type|sorted_participant_ids`. This handles the same match appearing on multiple wrestler profiles without double-counting.

## Data Pipeline

1. **Discovery** — crawler finds new wrestlers via promotion rosters and match participants
2. **Match Collection** — scrapes match history per wrestler, deduplicates, stores with participant links
3. **Profile Refresh** — periodic re-scrape of bios, socials, aliases, and promotion history
4. **ELO Calculation** — processes all matches chronologically, generates rating history
5. **Title Tracking** — scrapes title reign data per championship belt
6. **Validation** — compares DB match counts against source to detect gaps

## API Highlights

```bash
GET  /api/wrestlers              # All wrestlers (cached)
GET  /api/wrestlers/:id          # Full profile with socials & aliases
GET  /api/rankings               # Ranked list with tiers
GET  /api/elo-history            # Multi-wrestler ELO chart data
GET  /api/network/rivalries      # Most frequent opponent pairings
POST /api/predict/match          # ELO-based match outcome prediction
POST /api/predict/tournament     # Simulate tournament brackets
GET  /api/titles/:id             # Title history with reign data
GET  /api/scraper/status         # Crawler state + cron info
```

## Running Locally

```bash
git clone https://github.com/dashy-jngl/joshi-rankings-api.git
cd joshi-rankings-api
cp .env.example .env        # configure DB_PATH, ALLOWED_ORIGINS
go run main.go              # starts on :8080
```

Requires Go 1.21+ and CGO enabled (for SQLite).

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Language | Go | Fast compilation, goroutines for concurrent crawling, single binary deploy |
| Web Framework | Gin | Mature, middleware ecosystem, fast router |
| ORM | GORM | Auto-migrations, raw SQL escape hatch when needed |
| Database | SQLite (WAL) | Zero-ops, single-file backup, handles the read/write load fine |
| Auth | bcrypt + secure cookies | Session-based with CSRF origin checks |
| Hosting | Hetzner CX22 | €4/mo, 20TB traffic, fixed pricing |

## License

MIT

---

*Built by [dashy-jngl](https://github.com/dashy-jngl) 🇦🇺*
