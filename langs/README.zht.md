<p align="center">
  <img src="../docs/logo_2/icon.svg" alt="ZedSearch" width="128" height="128">
</p>

[![CI](https://github.com/vcaesar/zedsearch/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/vcaesar/zedsearch/actions/workflows/ci.yml)
[![Nightly](https://github.com/vcaesar/zedsearch/actions/workflows/nightly.yml/badge.svg?branch=main)](https://github.com/vcaesar/zedsearch/actions/workflows/nightly.yml)
[![Release](https://github.com/vcaesar/zedsearch/actions/workflows/release.yml/badge.svg)](https://github.com/vcaesar/zedsearch/actions/workflows/release.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/vcaesar/zedsearch.svg)](https://pkg.go.dev/github.com/vcaesar/zedsearch)
[![Go Version](https://img.shields.io/github/go-mod/go-version/vcaesar/zedsearch)](../go.mod)
[![golangci-lint](https://img.shields.io/badge/linter-golangci--lint-blue)](https://golangci-lint.run/)
[![Release](https://img.shields.io/github/v/release/vcaesar/zedsearch)](https://github.com/vcaesar/zedsearch/releases/latest)
[![Docs](https://img.shields.io/badge/Docs-Docs-green)](../docs/web/index.md) [![codecov](https://codecov.io/github/vcaesar/zedsearch/branch/main/graph/badge.svg)](https://codecov.io/github/vcaesar/zedsearch)
<a href="https://discord.gg/535uZPakJw"><img src="https://img.shields.io/discord/1484658282777018551.svg?logo=discord&logoColor=white&label=Discord&color=5865F2" alt="Join the Discord chat at https://discord.gg/535uZPakJw"></a>

[English](../README.md) | [简体中文](README.zh.md) | 繁體中文 | [日本語](README.ja.md) | [한국어](README.ko.md) | [Français](README.fr.md) | [Deutsch](README.de.md) | [Español](README.es.md) | [Русский](README.ru.md) | [Português](README.pt.md)

# ZedSearch

ZedSearch 是一個進行全文索引的搜尋引擎。它是 Elasticsearch 的輕量級替代方案，只需一小部分資源即可執行。它使用 [bluge](https://github.com/blugelabs/bluge)（透過 [vcaesar/riot](https://github.com/vcaesar/riot) 分支）作為底層索引函式庫。

相較於需要理解與調整數十個參數的 Elasticsearch，它非常簡單且容易維運。你可以在 2 分鐘內啟動並執行 ZedSearch。

如果你只是透過 API 寫入資料並使用 Kibana 進行搜尋，它可以直接取代 Elasticsearch（ZedSearch 不支援 Kibana，ZedSearch 提供自己的 UI）。

觀看下方影片，快速了解 ZedSearch 的展示。

<!-- [![Zed Youtube](../screenshots/zed-youtube.jpg)](https://www.youtube.com/watch?v=aZXtuVjt1ow) -->

# 為什麼選擇 ZedSearch

雖然 Elasticsearch 是一款非常優秀的產品，但它複雜、需要大量資源，而且已有十多年歷史。我打造 ZedSearch 是為了讓大家不必做大量工作就能更輕鬆地使用全文搜尋索引。

# 功能：

go + gin + react

1. 提供全文索引能力
2. 單一執行檔即可安裝與執行。Releases 中提供多種平台的執行檔。
3. 以 React 撰寫的資料查詢 Web UI（內嵌於執行檔中）
4. 相容 Elasticsearch 的資料寫入 API（單筆記錄與批次 API）
5. 開箱即用的身分驗證
6. 無結構描述（Schema less）—— 無需預先定義 schema，同一索引中的不同文件可以擁有不同的欄位。
7. 索引儲存於磁碟
8. 支援聚合
9. 可選擇啟用的[日誌索引](../pkg/logs/README.md)，支援事件時間正規化與日誌搜尋預設設定

# 效能測試

七個搜尋應用查詢，50,000 篇文件，回傳前 10 筆並計算精確總數，於 MacBook Pro M1 上測試，所有服務皆在 Docker 中執行：帶篩選的詞項查詢、兩個欄位的 OR、對全部文件依價格排序、第 101 頁、terms 與 histogram 聚合，以及醒目提示。除 QPS 外數值越低越好；「—」表示該引擎無法表達此查詢（Meilisearch 沒有數值直方圖，OpenObserve 不支援醒目提示）。

| 查詢 | ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
|---|---:|---:|---:|---:|---:|
| `filtered` (1 個用戶端) | **0.89 ms** | 1.38 ms | 1.44 ms | 1.87 ms | 9.44 ms |
| `filtered` (8 個用戶端) | **3,216 QPS** | 2,742 QPS | 3,066 QPS | 1,924 QPS | 321 QPS |
| `sort_price` (5 萬篇文件) | **0.74 ms** | 2.52 ms | 1.58 ms | 1.09 ms | 4.40 ms |
| `multi_field` title + body | **0.63 ms** | 1.44 ms | 1.51 ms | 1.19 ms | 21.0 ms |
| `deep_page` from 1000 | **1.24 ms** | 1.42 ms | 1.47 ms | 12.7 ms | 18.1 ms |
| `agg_terms` `category` | **1.48 ms** | 1.66 ms | 2.12 ms | 1.63 ms | 18.6 ms |
| `agg_histogram` `price` | **1.00 ms** | 1.67 ms | 1.72 ms | — | 18.7 ms |
| `highlight` `body` | **0.81 ms** | 1.96 ms | 1.97 ms | 1.92 ms | — |

### 單一查詢（v2）

同一批測試中的 v2 查詢，每種只測一種查詢類型。Meilisearch 無法表達 OR 與精確片語。

| 查詢 | ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
|---|---:|---:|---:|---:|---:|
| `term` rare | **0.50 ms** | 1.43 ms | 1.16 ms | 1.14 ms | 15.7 ms |
| `term` mid | **0.58 ms** | 1.28 ms | 1.27 ms | 1.16 ms | 17.6 ms |
| `term` keyword | **0.57 ms** | 1.75 ms | 2.32 ms | 1.03 ms | 5.59 ms |
| `bool` should | **0.63 ms** | 1.61 ms | 1.75 ms | — | 26.7 ms |
| `bool` must | **1.01 ms** | 2.36 ms | 3.30 ms | 1.28 ms | 24.1 ms |
| `range` | **0.64 ms** | 1.32 ms | 1.28 ms | 1.32 ms | 5.31 ms |
| `match_all` | **0.66 ms** | 1.48 ms | 1.38 ms | 1.03 ms | 4.68 ms |
| `prefix` `w1*` | **0.71 ms** | 1.87 ms | 2.66 ms | 1.32 ms | 11.9 ms |
| `match_phrase` | **10.3 ms** | 12.7 ms | 12.6 ms | — | 16.3 ms |
| `bool` should (8 個用戶端) | **5,179 QPS** | 2,707 QPS | 2,616 QPS | — | 131 QPS |

# 文件

文件位於 [docs/web](../docs/web/index.md)，API 文件：[zedsearch-docs](https://github.com/zedsearch/zedsearch-docs)

# 螢幕截圖

## 搜尋畫面

![Search screen](../docs/hots/search-26.png)

## 索引畫面

![Index screen](../docs/hots/index-26.png)

# 入門

## 快速開始

請參閱 [快速開始](../docs/web/quickstart.md)

# 發行版本

ZedSearch 已有數百個正式環境部署。

各平台的二進位檔可在 [Releases 頁面](https://github.com/vcaesar/zedsearch/releases/latest) 取得。預設封存檔未使用 UPX 壓縮；為了更小的下載大小，所有 Linux 版本與 Windows amd64 版本另外提供經 [UPX](https://upx.github.io/) 壓縮的 `*_upx.tar.gz`（啟動稍慢，且部分防毒軟體可能會誤判壓縮過的 `.exe` 檔）。

## 建置

設定 `ZED_FIRST_ADMIN_USER` / `ZED_FIRST_ADMIN_PASSWORD` 或設定 `conf/zed.toml`（見下文），然後在儲存庫根目錄執行：

```sh
go run cmd/zedsearch/main.go      # API-only, :4080
go run cmd/zedsearch/main.go web  # with the web UI (Node.js 24+ and npm)
```

`web` 會產生 `test/zedsearch-web`，一個內嵌 UI 的獨立二進位檔。

### 設定

設定依以下順序生效：內建預設值（[`pkg/config/default.toml`](../pkg/config/default.toml)），接著是工作目錄下選用的 `conf/zed.toml`，最後是非空的環境變數。每個鍵名轉為大寫即為對應的環境變數，例如 `zed_server_port` → `ZED_SERVER_PORT`。

```toml
# conf/zed.toml
zed_first_admin_user = "admin"
zed_first_admin_password = "change-me"
zed_server_port = "4080"
zed_data_path = "./data"
zed_log_level = "info"

[Shard]
zed_shard_num = 3
```

- 只需列出要修改的鍵；分組的鍵需保留 `default.toml` 中的表頭（`[Shard]`、`[S3]`、`[Etcd]` 等）。
- 未知的鍵會導致啟動失敗。時間長度單位為奈秒，大小單位為位元組。
- 首位管理員憑證僅在首次啟動時用於建立管理員使用者；登入後請變更密碼，切勿提交真實憑證。

# 社群

- 如何開發並為 ZedSearch 做出貢獻

  請參閱[貢獻指南](../CONTRIBUTING.md)。另請參閱[路線圖](../docs/web/roadmap.md)

# 範例

你可以使用 ZedSearch 索引與搜尋任何資料。以下是一些社群成員使用 zedsearch 索引與搜尋 Enron 電子郵件資料集的範例：

1. https://github.com/jorgeloaiza48/Enron-Email-DataSet
1. https://github.com/jhojanperlaza/email_search_engine
1. https://github.com/carlosarraes/zinmail
1. https://github.com/devjopa/golab-search
1. https://github.com/avaco2312/zincsearch
1. https://github.com/paolorossig/email-indexer
1. https://github.com/ulimonte05/zincsearching
