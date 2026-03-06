# Joshi Rankings API — Roadmap 🗺️

## What's Done ✅
- Go REST API with Gin + GORM + SQLite
- Models: Wrestler, WrestlerAlias, Match, MatchParticipant, ELOHistory, Ranking, Trends
- Handlers: Wrestler CRUD, Match CRUD (with ELO calc in transaction), Rankings (with trends)
- Services: ELO calculator (singles + multi + draws), Rankings builder (trends + streaks)
- CORS middleware, seed data (16 wrestlers), Makefile, .env.example, .air.toml
- Project structure clean and complete

---

## Phase 1 — Compile & Test Locally
**Goal:** Get the API running and verify all endpoints work.

### Tasks
- [ ] `go mod tidy` — resolve all dependencies
- [ ] Fix any compilation errors
- [ ] Run the API (`go run main.go` or `make run`)
- [ ] Test every endpoint with curl:
  - `GET /api/wrestlers` — should return seeded wrestlers
  - `POST /api/matches` — create a singles match, verify ELO updates
  - `POST /api/matches` — create a tag match, verify team scaling
  - `POST /api/matches` — create a draw, verify draw handling
  - `GET /api/rankings` — verify sorted by ELO with trends
  - `GET /api/rankings?promotion=stardom` — verify filter
  - `DELETE /api/matches/:id` — verify deletion
- [ ] Fix any runtime bugs found during testing

### Estimated time: 1 session

---

## Phase 2 — Auth & Route Protection ✅ DONE
**Goal:** Lock down write endpoints so only we (and the scrapers) can modify data. GET stays public.

### Design
- **API key auth** — simple, no user accounts needed
- Key sent via `X-API-Key` header
- Key stored as `API_KEY` environment variable
- Only protect: POST, PUT, DELETE routes

### Tasks
- [ ] Create `middleware/auth.go`:
  ```go
  func APIKeyAuth() gin.HandlerFunc {
      return func(c *gin.Context) {
          key := c.GetHeader("X-API-Key")
          expected := os.Getenv("API_KEY")
          if expected == "" {
              // No key configured = dev mode, allow all
              c.Next()
              return
          }
          if key != expected {
              c.AbortWithStatusJSON(401, gin.H{"error": "Unauthorized"})
              return
          }
          c.Next()
      }
  }
  ```
- [ ] Apply middleware to write routes in `main.go`:
  ```go
  // Public (no auth)
  api.GET("/wrestlers", ...)
  api.GET("/wrestlers/:id", ...)
  api.GET("/matches", ...)
  api.GET("/rankings", ...)

  // Protected (API key required)
  protected := api.Group("/")
  protected.Use(middleware.APIKeyAuth())
  {
      protected.POST("/wrestlers", ...)
      protected.PUT("/wrestlers/:id", ...)
      protected.DELETE("/wrestlers/:id", ...)
      protected.POST("/matches", ...)
      protected.DELETE("/matches/:id", ...)
  }
  ```
- [ ] Add `API_KEY` to `.env.example`
- [ ] Test: requests without key get 401, with key get through

### Estimated time: 30 mins

---

## Phase 3 — PostgreSQL Migration
**Goal:** Swap SQLite for PostgreSQL for production readiness.

### Why
- SQLite is fine for dev but doesn't scale for concurrent scraper writes
- PostgreSQL is what production Go apps use
- Fly.io has managed Postgres (free tier)

### Tasks
- [ ] Add `gorm.io/driver/postgres` to go.mod
- [ ] Update `database/db.go` to support both drivers:
  ```go
  func InitDB() (*gorm.DB, error) {
      dsn := os.Getenv("DATABASE_URL")
      if dsn == "" {
          // Fallback to SQLite for local dev
          return gorm.Open(sqlite.Open("joshi.db"), &gorm.Config{})
      }
      return gorm.Open(postgres.Open(dsn), &gorm.Config{})
  }
  ```
- [ ] Update `main.go` to use new InitDB (no path param)
- [ ] Test locally with SQLite still works
- [ ] Test with a local PostgreSQL (if available) or defer to deploy phase
- [ ] Add `DATABASE_URL` to `.env.example`

### Estimated time: 30 mins

---

## Phase 4 — Scrapers
**Goal:** Auto-scrape official promotion sites for match results, parse them, and update rankings.

### Architecture
```
main.go starts:
├── Gin HTTP server (API)
└── Scraper scheduler (background goroutine)
    ├── Runs on startup + every 24hrs
    ├── Fans out: one goroutine per promotion (concurrent)
    ├── Each scraper: fetch → parse → return []RawMatch
    └── Processor: match wrestlers to DB, dedup, insert, recalc ELO
```

### Scraper Interface
```go
// scraper/scraper.go
type ResultsScraper interface {
    Name() string
    FetchResults() ([]RawMatch, error)
}

type RawMatch struct {
    MatchType    string
    EventName    string
    Date         time.Time
    IsTitleMatch bool
    Promotion    string
    Participants []RawParticipant
}

type RawParticipant struct {
    Name     string
    Team     int
    IsWinner bool
}
```

### Target Promotions
| Priority | Promotion | Source URL | Notes |
|----------|-----------|-----------|-------|
| 1 | Stardom | https://wwr-stardom.com/result/ | Most important, Dashy's fave |
| 2 | TJPW | https://www.ddtpro.com/tjpw/results | DDT family, good data |
| 3 | Marvelous | https://www.marvelous-pro.com/ | Smaller, less frequent |

### Tasks
- [ ] Create `scraper/scraper.go` — interface + RawMatch types + scheduler
- [ ] Create `scraper/processor.go` — handles:
  - Wrestler name matching (exact → alias → fuzzy/Levenshtein)
  - Auto-create unknown wrestlers at 1200 ELO
  - Deduplication (hash of event+date+participants)
  - Insert match + trigger ELO recalc via existing handler logic
- [ ] Create `scraper/stardom.go` — implements ResultsScraper
  - Fetch results page, parse event cards
  - Extract: event name, date, match type, participants, winners
  - Respect rate limits: 1 req / 3 seconds, proper User-Agent
- [ ] Create `scraper/tjpw.go` — implements ResultsScraper
- [ ] Create `scraper/marvelous.go` — implements ResultsScraper (if site is parseable)
- [ ] Add scraper startup to `main.go`:
  ```go
  scrapers := []scraper.ResultsScraper{
      scraper.NewStardomScraper(),
      scraper.NewTJPWScraper(),
  }
  scraper.StartScheduler(db, scrapers)
  ```
- [ ] Add scraper status/control endpoints:
  ```
  GET  /api/scraper/status   — last run, results per promotion
  POST /api/scraper/run      — manual trigger (protected by API key)
  ```
- [ ] Create `scraper/log.go` — store scrape run history in DB
- [ ] Add ScrapeLog model:
  ```go
  type ScrapeLog struct {
      ID          uint
      Promotion   string
      MatchesFound int
      NewMatches   int
      Errors       string
      RunAt        time.Time
  }
  ```
- [ ] Test: run scraper manually, verify matches appear in DB with correct ELO

### Scraping Rules (CRITICAL)
- **Primary sources ONLY** — official promotion websites
- **Cagematch is LAST RESORT** — only if a promotion has no usable site
- **Respectful:** min 3s between requests, identify as bot
- **Error isolation:** one promotion failing doesn't affect others
- **Data integrity:** incorrect data is worse than missing data

### Estimated time: 2-3 sessions (biggest phase)

---

## Phase 5 — Frontend
**Goal:** A web UI showing rankings, wrestler profiles, and ELO history charts.

### Stack Decision
- **React** (Dashy already knows it) OR
- **Next.js** (React + SSR, looks good on resume, Tokyo companies love it)
- **Tailwind CSS** for styling
- **Recharts or Chart.js** for ELO history graphs

### Pages
| Page | Route | Description |
|------|-------|-------------|
| Rankings | `/` | Main leaderboard, sortable, filterable by promotion |
| Wrestler Profile | `/wrestler/:id` | Full stats, ELO history chart, recent matches, W/L record |
| Recent Matches | `/matches` | Chronological match list with ELO changes shown |
| Head-to-Head | `/h2h?w1=X&w2=Y` | Compare two wrestlers (stretch goal) |
| About | `/about` | What this is, how ELO works |

### Features
- ELO history line chart per wrestler (the money feature for interviews)
- Trend arrows (↑↓→) on rankings
- Win/loss streak badges
- Promotion logos/colours
- Responsive (mobile-friendly)
- Dark mode (wrestling vibes)

### Tasks
- [ ] Decide: React or Next.js (discuss with Dashy)
- [ ] Scaffold project in `joshi-rankings-api/frontend/` or separate repo
- [ ] Rankings page — table with rank, name, ELO, promotion, trend arrows, streak
- [ ] Wrestler profile page — stats card + ELO chart + match history
- [ ] Matches page — chronological list with ELO deltas
- [ ] Add `/api/wrestlers/:id/history` endpoint to API (ELO history for charts)
- [ ] Add `/api/head-to-head?w1=X&w2=Y` endpoint (stretch)
- [ ] Style with Tailwind, dark theme
- [ ] Mobile responsive
- [ ] Build + serve (static export or SSR depending on stack)

### Estimated time: 2-3 sessions

---

## Phase 6 — Deploy
**Goal:** Get the full stack live on the internet with a URL for the resume.

### Tasks
- [ ] Choose hosting (Fly.io / Railway / Render / VPS)
- [ ] Set up PostgreSQL in production
- [ ] Configure environment variables (DATABASE_URL, API_KEY, PORT)
- [ ] Deploy API
- [ ] Deploy frontend (same host or separate — Vercel for Next.js is free)
- [ ] Set up custom domain (optional but nice)
- [ ] Verify scrapers run on schedule in production
- [ ] Write a killer README.md with:
  - Live demo link
  - Screenshots
  - Tech stack badges
  - Architecture diagram
  - API docs with curl examples
  - "How ELO works" section
  - Local dev setup instructions
- [ ] Add to GitHub (public repo)
- [ ] Add to resume + cover letters

### Estimated time: 1 session

---

## Phase 7 — Stretch Goals (Post-Deploy)
- [ ] Championship tracking (who holds what titles, title history)
- [ ] Match ratings (user-submitted star ratings)
- [ ] Promotion power rankings (average ELO per roster)
- [ ] Prediction engine ("who would win?")
- [ ] WebSocket live updates when new matches are scraped
- [ ] RSS/webhook notifications for big upsets
- [ ] Historical data import (backfill past matches)

---

## Session Quick Reference
When starting a new session, tell Suzu:
> "Let's work on joshi-rankings-api, Phase X"

She'll read this roadmap and know exactly where we're at. Check off tasks as we go.

---

*Last updated: 2026-02-18*
*Project: /home/dash/clawd/joshi-rankings-api/*
*Spec: /home/dash/clawd/joshi-rankings-api-spec.md*
