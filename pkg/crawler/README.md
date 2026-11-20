# Crawler

A small HTTP-first Go crawler for AI retrieval pipelines and ZedSearch indexing.
Import `github.com/vcaesar/zedsearch/pkg/crawler`.

## Crawl → rank → index → search

```sh
# 1. start ZedSearch with the console (or `cd frontend && npm run dev` against a running server)
ZED_FIRST_ADMIN_USER=admin ZED_FIRST_ADMIN_PASSWORD=*** go run ./cmd/zedsearch web
# 2. crawl, PageRank and bulk-index into index "web"
ZED_FIRST_ADMIN_PASSWORD=*** go run ./cmd/zedcrawl -max-pages 200 -depth 2 https://go.dev/
```

Each document stores `url`, `host`, `title`, `description`, `content`, `content_hash`,
`out_links`, `page_rank` (mean 1 over the crawl) and `@timestamp` (fetch time); the ID is a
hash of the URL, so re-crawls upsert.
In the console Search view, a non-empty query ranks
hits by `_score × sqrt(page_rank)` (`function_score`; indexes without `page_rank` keep plain
BM25). Pick a time range covering the crawl, or "Full time".
