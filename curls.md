| Endpoint                    | What it does                                                          |
| --------------------------- | --------------------------------------------------------------------- |
| `POST /scraper/collect`     | Scrape + store matches (no ELO). Safe to run repeatedly — skips dupes |
| `POST /scraper/recalculate` | Reset all ELO → replay chronologically. Run when dataset is ready     |
| `POST /scraper/cron/start`  | Start 6hr auto-scrape (incremental mode)                              |
| `POST /scraper/cron/stop`   | Stop the cron                                                         |
| `GET /scraper/status`       | Last run time, match count, cron running?                             |

1. POST /scraper/collect ← run this a few times over days
2. POST /scraper/collect ← keep running until dataset looks complete
3. POST /scraper/recalculate ← once happy, calculate all ELO
4. POST /scraper/cron/start ← turn on auto-updates
