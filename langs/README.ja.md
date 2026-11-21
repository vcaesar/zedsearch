<p align="center">
  <img src="../docs/logo_2/icon.svg" alt="ZedSearch" width="128" height="128">
</p>

[![CI](https://github.com/vcaesar/zedsearch/actions/workflows/ci.yml/badge.svg?branch=dev)](https://github.com/vcaesar/zedsearch/actions/workflows/ci.yml)
[![Nightly](https://github.com/vcaesar/zedsearch/actions/workflows/nightly.yml/badge.svg?branch=dev)](https://github.com/vcaesar/zedsearch/actions/workflows/nightly.yml)
[![Release](https://github.com/vcaesar/zedsearch/actions/workflows/release.yml/badge.svg)](https://github.com/vcaesar/zedsearch/actions/workflows/release.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/vcaesar/zedsearch.svg)](https://pkg.go.dev/github.com/vcaesar/zedsearch)
[![Go Version](https://img.shields.io/github/go-mod/go-version/vcaesar/zedsearch)](../go.mod)
[![golangci-lint](https://img.shields.io/badge/linter-golangci--lint-blue)](https://golangci-lint.run/)
[![Release](https://img.shields.io/github/v/release/vcaesar/zedsearch)](https://github.com/vcaesar/zedsearch/releases/latest)
[![Docs](https://img.shields.io/badge/Docs-Docs-green)](../docs/web/index.md) [![codecov](https://codecov.io/gh/vcaesar/zedsearch/graph/badge.svg)](https://app.codecov.io/gh/vcaesar/zedsearch)
<a href="https://discord.gg/535uZPakJw"><img src="https://img.shields.io/discord/1484658282777018551.svg?logo=discord&logoColor=white&label=Discord&color=5865F2" alt="Join the Discord chat at https://discord.gg/535uZPakJw"></a>

[English](../README.md) | [简体中文](README.zh.md) | [繁體中文](README.zht.md) | 日本語 | [한국어](README.ko.md) | [Français](README.fr.md) | [Deutsch](README.de.md) | [Español](README.es.md) | [Русский](README.ru.md) | [Português](README.pt.md)

# ZedSearch

ZedSearch は全文インデックスを行う検索エンジンです。Elasticsearch の軽量な代替であり、わずかなリソースで動作します。基盤となるインデックスライブラリとして [bluge](https://github.com/blugelabs/bluge)（[vcaesar/riot](https://github.com/vcaesar/riot) フォーク経由）を使用しています。

理解や調整に何十ものパラメータを必要とする Elasticsearch とは異なり、非常にシンプルで運用が簡単です。ZedSearch は 2 分で起動して動かすことができます。

API でデータを取り込み、Kibana で検索するだけであれば、Elasticsearch のドロップイン置き換えとして使えます（ZedSearch は Kibana をサポートしていません。ZedSearch は独自の UI を提供します）。

ZedSearch の簡単なデモは以下の動画をご覧ください。

<!-- [![Zed Youtube](../screenshots/zed-youtube.jpg)](https://www.youtube.com/watch?v=aZXtuVjt1ow) -->

## Homebrew

macOS と Linux では [vcaesar/homebrew-tap](https://github.com/vcaesar/homebrew-tap) からインストールできます：

```sh
brew install vcaesar/tap/zedsearch
```

## Docker

イメージは [Docker Hub](https://hub.docker.com/r/riotgo/zedsearch) で `riotgo/zedsearch` として公開されています（linux/amd64、linux/arm64、linux/arm/v7）：

実行する前に、環境変数 `ZED_FIRST_ADMIN_PASSWORD` に任意のパスワードを設定してください：

```sh
docker run -d --name zedsearch \
    -p 4080:4080 \
    -v zedsearch-data:/data -e ZED_DATA_PATH=/data \
    -e ZED_FIRST_ADMIN_USER="admin" \
    -e ZED_FIRST_ADMIN_PASSWORD="your_password_zed#123" \
    riotgo/zedsearch:latest
```

名前付きボリューム `zedsearch-data` はコンテナのユーザー（uid 10001）が書き込めます。ホストのディレクトリを使う場合は、`mkdir -p ./data && sudo chown 10001:10001 ./data` を実行してから `-v "$PWD/data:/data"` でマウントします。

# なぜ ZedSearch なのか

Elasticsearch は非常に優れた製品ですが、複雑で多くのリソースを必要とし、10 年以上前から存在しています。私は、多くの作業をせずに誰もが全文検索インデックスを簡単に使えるように ZedSearch を作りました。

# 機能：

go + gin（`-tags fiber` で fiber v3 に切り替え可能）+ react

1. 高速なクエリと小さなインデックスファイル
2. 全文インデックスとベクトルインデックス機能を提供
3. インストールと実行は単一バイナリで完結。複数プラットフォーム向けのバイナリを Releases で提供。
4. データをクエリするための React 製 Web UI（バイナリに埋め込み）
5. データ取り込み用の Elasticsearch 互換 API（単一レコードおよびバルク API）
6. すぐに使える認証機能
7. スキーマレス - 事前にスキーマを定義する必要がなく、同じインデックス内の異なるドキュメントが異なるフィールドを持てます。
8. インデックスはディスク、S3、MinIO などに保存可能
9. 集計（aggregation）と Elasticsearch の豊富な機能をサポート
10. 多言語対応、CJK（中国語・日本語・韓国語）をサポート
11. イベント時刻の正規化とログ検索のデフォルト設定を備え、JSON、Splunk HEC、Loki、OTLP/HTTP JSON（OpenObserve 互換）および Logstash のイベントを取り込める、オプトインの[ログインデックス](../pkg/logs/README.md)
12. kNN ベクトル検索と地理クエリ（`geo_distance`、`geo_bounding_box`、`geo_polygon`、`geo_shape`）
13. Elasticsearch SQL API（`/es/_sql`、`/es/_sql/translate`）と、MySQL の `CREATE TABLE` [スキーマをマッピングとして利用](../docs/sql-mappings.md)
14. 高可用性[クラスター](../pkg/cluster/README.md)：etcd で調整されるパーティション所有権、フェンシング付きハンドオフ、レプリカ、scatter-gather 検索
15. OTLP/HTTP による OpenTelemetry トレーシング（`zed_tracing_enable`）。すべてのリクエストにノードをまたいで引き継がれる `X-Trace-Id` を付与
16. Fleet と `zedagent`（`agent/`）：エージェントの登録、ポリシー管理、ログファイルとシステムメトリクスの送信、スタンドアロン実行にも対応
17. robots.txt を尊重する[Web クローラー](../pkg/crawler/README.md)（`cmd/zedcrawl`）、PageRank 計算と ZedSearch へのバルク投入に対応

# ベンチマーク

50,000 件のドキュメントに対する検索アプリケーション向けの 7 つのクエリ（上位 10 件と正確な総ヒット数）。MacBook Pro M1 上で、すべてのサーバーを Docker で実行しています。フィルター付き term クエリ、2 フィールドの OR、全ドキュメントの価格ソート、101 ページ目、terms と histogram の集計、ハイライトです。QPS 以外は低いほど良く、「—」はそのエンジンでは表現できないクエリです（Meilisearch には数値ヒストグラムがなく、OpenObserve はハイライトしません）。

| クエリ                      |     ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
| --------------------------- | ------------: | ----------------: | ----------------: | -------------: | ---------------: | --------------: |
| `filtered` (1 クライアント) |   **0.89 ms** |           3.43 ms |           1.38 ms |        1.44 ms |          1.87 ms |         9.44 ms |
| `filtered` (8 クライアント) | **3,216 QPS** |           471 QPS |         2,742 QPS |      3,066 QPS |        1,924 QPS |         321 QPS |
| `sort_price` (5 万件)       |   **0.74 ms** |           76.0 ms |           2.52 ms |        1.58 ms |          1.09 ms |         4.40 ms |
| `multi_field` title + body  |   **0.63 ms** |           2.04 ms |           1.44 ms |        1.51 ms |          1.19 ms |         21.0 ms |
| `deep_page` from 1000       |   **1.24 ms** |           3.40 ms |           1.42 ms |        1.47 ms |          12.7 ms |         18.1 ms |
| `agg_terms` `category`      |   **1.48 ms** |           5.55 ms |           1.66 ms |        2.12 ms |          1.63 ms |         18.6 ms |
| `agg_histogram` `price`     |   **1.00 ms** |           20.1 ms |           1.67 ms |        1.72 ms |                — |         18.7 ms |
| `highlight` `body`          |   **0.81 ms** |           2.27 ms |           1.96 ms |        1.97 ms |          1.92 ms |               — |

### 単一クエリ（v2）

同じセッションで実行した v2 のクエリで、それぞれ 1 種類のクエリだけを測ります。Meilisearch は OR と完全一致フレーズを表現できません。

| クエリ                         |     ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
| ------------------------------ | ------------: | ----------------: | ----------------: | -------------: | ---------------: | --------------: |
| `term` rare                    |   **0.50 ms** |           1.03 ms |           1.43 ms |        1.16 ms |          1.14 ms |         15.7 ms |
| `term` mid                     |   **0.58 ms** |           1.82 ms |           1.28 ms |        1.27 ms |          1.16 ms |         17.6 ms |
| `term` keyword                 |   **0.57 ms** |           0.82 ms |           1.75 ms |        2.32 ms |          1.03 ms |         5.59 ms |
| `bool` should                  |   **0.63 ms** |           1.12 ms |           1.61 ms |        1.75 ms |                — |         26.7 ms |
| `bool` must                    |   **1.01 ms** |           1.79 ms |           2.36 ms |        3.30 ms |          1.28 ms |         24.1 ms |
| `range`                        |   **0.64 ms** |           2.56 ms |           1.32 ms |        1.28 ms |          1.32 ms |         5.31 ms |
| `match_all`                    |   **0.66 ms** |           4.12 ms |           1.48 ms |        1.38 ms |          1.03 ms |         4.68 ms |
| `prefix` `w1*`                 |   **0.71 ms** |          214.5 ms |           1.87 ms |        2.66 ms |          1.32 ms |         11.9 ms |
| `match_phrase`                 |   **10.3 ms** |           55.6 ms |           12.7 ms |        12.6 ms |                — |         16.3 ms |
| `bool` should (8 クライアント) | **5,179 QPS** |         1,292 QPS |         2,707 QPS |      2,616 QPS |                — |         131 QPS |

### リソース使用量

Docker イメージ（非圧縮）、同じ 50,000 件のドキュメントのテキストインデックス（`_flush` 後、ZedSearch・Elasticsearch・OpenSearch はいずれも約 25 MB）、実行全体のメモリ / CPU（100% = 1 vCPU）。データは 3 回の実行の中央値です。

| リソース                       |   ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 |               OpenObserve 1.1 |
| ------------------------------ | ----------: | ----------------: | ----------------: | -------------: | ---------------: | ----------------------------: |
| Docker イメージ                | **56.4 MB** |           72.4 MB |          1,195 MB |       1,311 MB |           370 MB |                        434 MB |
| テキストインデックス（5 万件） |     25.9 MB |          133.3 MB |           25.8 MB |        25.7 MB |           454 MB | 14.2 MB（インデックス未作成） |
| アイドル時メモリ               |  **14 MiB** |            64 MiB |         2,674 MiB |      2,699 MiB |          179 MiB |                       387 MiB |
| ピークメモリ                   | **388 MiB** |           858 MiB |         3,185 MiB |      3,191 MiB |        1,014 MiB |                       919 MiB |
| 平均 CPU                       |    **134%** |              366% |              157% |           156% |             230% |                          140% |
| ピーク CPU                     |        553% |              710% |              620% |           529% |             545% |                      **519%** |

# ドキュメント

ドキュメントは [docs/web](../docs/web/index.md)、API ドキュメントは [zedsearch-docs](https://github.com/zedsearch/zedsearch-docs) で参照できます。

# スクリーンショット

## 検索画面

![Search screen](../docs/hots/search-26.png)

## インデックス画面

![Index screen](../docs/hots/index-26.png)

# はじめに

## クイックスタート

[クイックスタート](../docs/web/quickstart.md)を参照してください

# リリース

ZedSearch は数百の本番環境で導入されています。

各プラットフォーム向けのバイナリは [Releases ページ](https://github.com/vcaesar/zedsearch/releases/latest) にあります。デフォルトのアーカイブは UPX で圧縮されていません。ダウンロードサイズを抑えたい場合は、すべての Linux ビルドと Windows amd64 ビルドに [UPX](https://upx.github.io/) で圧縮した `*_upx.tar.gz` も用意しています（起動がやや遅くなり、一部のウイルス対策ソフトが圧縮された `.exe` ファイルを誤検知する場合があります）。

## ビルド

`ZED_FIRST_ADMIN_USER` / `ZED_FIRST_ADMIN_PASSWORD` を設定するか `conf/zed.toml` を用意し（後述）、リポジトリのルートで実行します：

```sh
go run cmd/zedsearch/main.go      # API-only, :4080
go run cmd/zedsearch/main.go web  # with the web UI (Node.js 24+ and npm)
```

`web` は UI を組み込んだ単体バイナリ `test/zedsearch-web` を生成します。

### 設定

設定は次の順に適用されます：組み込みのデフォルト（[`pkg/config/default.toml`](../pkg/config/default.toml)）、作業ディレクトリにある任意の `conf/zed.toml`、空でない環境変数。各キーを大文字にしたものが環境変数名になります（例：`zed_server_port` → `ZED_SERVER_PORT`）。

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

- 変更するキーだけを記述します。グループ化されたキーは `default.toml` のテーブルヘッダー（`[Shard]`、`[S3]`、`[Etcd]` など）を付けて記述します。
- 不明なキーがあると起動に失敗します。期間はナノ秒、サイズはバイト単位です。
- 初回管理者の認証情報は初回起動時の管理者ユーザー作成にのみ使われます。ログイン後にパスワードを変更し、実際の認証情報はコミットしないでください。

# コミュニティ

- ZedSearch の開発とコントリビュートの方法

  [コントリビューションガイド](../CONTRIBUTING.md)を確認してください。[ロードマップ](../docs/web/roadmap.md)もご覧ください

# 例

ZedSearch を使ってあらゆるデータをインデックス化・検索できます。以下は、zedsearch を使って Enron メールデータセットをインデックス化・検索した例です：

1. https://github.com/jorgeloaiza48/Enron-Email-DataSet
1. https://github.com/jhojanperlaza/email_search_engine
1. https://github.com/carlosarraes/zinmail
1. https://github.com/devjopa/golab-search
1. https://github.com/avaco2312/zincsearch
1. https://github.com/paolorossig/email-indexer
1. https://github.com/ulimonte05/zincsearching
