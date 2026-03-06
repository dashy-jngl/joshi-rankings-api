.PHONY: build run dev test clean seed scrape

# Build the binary
build:
	go build -o joshi-rankings-api .

# Run the server
run: build
	./joshi-rankings-api

# Run with hot reload (requires air: go install github.com/air-verse/air@latest)
dev:
	air

# Run tests
test:
	go test ./... -v

# Test with coverage
test-cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out

# Clean build artifacts
clean:
	rm -f joshi-rankings-api joshi.db coverage.out

# Seed the database with initial wrestlers
seed:
	go run . --seed

# Trigger a manual scrape
scrape:
	curl -X POST http://localhost:8080/api/scraper/run
