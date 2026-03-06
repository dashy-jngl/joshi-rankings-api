# Joshi Rankings API — Endpoint Reference

Base URL: `http://localhost:8080/api`

## Wrestlers

| Method | Path | Description |
|--------|------|-------------|
| GET | `/wrestlers` | List all wrestlers. Query: `?promotion=stardom` |
| GET | `/wrestlers/:id` | Get wrestler by ID with full stats |
| POST | `/wrestlers` | Create wrestler. Body: `{ name, promotion }` |
| PUT | `/wrestlers/:id` | Update wrestler info |
| DELETE | `/wrestlers/:id` | Delete wrestler |

## Matches

| Method | Path | Description |
|--------|------|-------------|
| GET | `/matches` | List matches. Query: `?wrestler_id=1` |
| POST | `/matches` | Log match result → triggers ELO recalc |
| DELETE | `/matches/:id` | Delete match → triggers ELO recalc |

### Match Body
```json
{
  "winner_id": 6,
  "loser_id": 4,
  "match_type": "singles",
  "event_name": "Stardom New Year Stars 2026",
  "date": "2026-01-03T00:00:00Z",
  "is_title_match": true
}
```

## Rankings

| Method | Path | Description |
|--------|------|-------------|
| GET | `/rankings` | Power rankings. Query: `?promotion=`, `?limit=` |

## Scraper

| Method | Path | Description |
|--------|------|-------------|
| GET | `/scraper/status` | Last run time + results per promotion |
| POST | `/scraper/run` | Trigger manual scrape |
| GET | `/scraper/log` | Recent scrape activity |

## Stats (stretch)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/wrestlers/:id/history` | ELO rating history over time |
| GET | `/head-to-head?w1=1&w2=2` | Head-to-head record |

## Response Format

All responses are JSON. Errors return:
```json
{
  "error": "description of what went wrong"
}
```

## Status Codes

| Code | Meaning |
|------|---------|
| 200 | OK |
| 201 | Created |
| 400 | Bad request (validation error) |
| 404 | Not found |
| 500 | Server error |
