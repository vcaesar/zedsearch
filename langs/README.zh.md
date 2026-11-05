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

[English](../README.md) | 简体中文 | [繁體中文](README.zht.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Français](README.fr.md) | [Deutsch](README.de.md) | [Español](README.es.md) | [Русский](README.ru.md) | [Português](README.pt.md)

# ZedSearch

ZedSearch 是一个进行全文索引的搜索引擎。它是 Elasticsearch 的轻量级替代品，只需一小部分资源即可运行。它使用 [bluge](https://github.com/blugelabs/bluge)（通过 [vcaesar/riot](https://github.com/vcaesar/riot) 分支）作为底层索引库。

与需要理解和调整几十个参数的 Elasticsearch 相比，它非常简单且易于运维。你可以在 2 分钟内启动并运行 ZedSearch。

如果你只是通过 API 写入数据并使用 Kibana 进行搜索，它可以直接替代 Elasticsearch（ZedSearch 不支持 Kibana，ZedSearch 提供了自己的 UI）。

观看下面的视频，快速了解 ZedSearch 的演示。

<!-- [![Zed Youtube](../screenshots/zed-youtube.jpg)](https://www.youtube.com/watch?v=aZXtuVjt1ow) -->

# 为什么选择 ZedSearch

虽然 Elasticsearch 是一款非常优秀的产品，但它复杂、需要大量资源，而且已有十多年历史。我构建 ZedSearch 是为了让大家无需做大量工作就能更轻松地使用全文搜索索引。

# 特性：

go + gin + react

1. 提供全文索引能力
2. 单个二进制文件即可安装和运行。Releases 中提供多个平台的二进制文件。
3. 使用 React 编写的数据查询 Web UI（嵌入在二进制文件中）
4. 兼容 Elasticsearch 的数据写入 API（单条记录和批量 API）
5. 开箱即用的身份认证
6. 无模式（Schema less）—— 无需预先定义 schema，同一索引中的不同文档可以拥有不同的字段。
7. 索引存储在磁盘上
8. 支持聚合
9. 可选启用的[日志索引](../pkg/logs/README.md)，支持事件时间规范化和日志搜索默认配置

# 基准测试

七个搜索应用查询，50,000 篇文档，返回前 10 条并统计精确总数，在 MacBook Pro M1 上测试，所有服务均运行在 Docker 中：带过滤的词项查询、两个字段的 OR、对全部文档按价格排序、第 101 页、terms 和 histogram 聚合，以及高亮。除 QPS 外数值越低越好；“—”表示该引擎无法表达此查询（Meilisearch 没有数值直方图，OpenObserve 不支持高亮）。

| 查询 | ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
|---|---:|---:|---:|---:|---:|
| `filtered` (1 个客户端) | **0.89 ms** | 1.38 ms | 1.44 ms | 1.87 ms | 9.44 ms |
| `filtered` (8 个客户端) | 2,959 QPS | 2,742 QPS | **3,066 QPS** | 1,924 QPS | 321 QPS |
| `sort_price` (5 万篇文档) | 1.85 ms | 2.52 ms | 1.58 ms | **1.09 ms** | 4.40 ms |
| `multi_field` title + body | **0.63 ms** | 1.44 ms | 1.51 ms | 1.19 ms | 21.0 ms |
| `deep_page` from 1000 | **1.24 ms** | 1.42 ms | 1.47 ms | 12.7 ms | 18.1 ms |
| `agg_terms` `category` | **1.48 ms** | 1.66 ms | 2.12 ms | 1.63 ms | 18.6 ms |
| `agg_histogram` `price` | **1.00 ms** | 1.67 ms | 1.72 ms | — | 18.7 ms |
| `highlight` `body` | **0.81 ms** | 1.96 ms | 1.97 ms | 1.92 ms | — |

### 单一查询（v2）

同一批测试中的 v2 查询，每种只测一种查询类型。Meilisearch 无法表达 OR 和精确短语。

| 查询 | ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
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
| `bool` should (8 个客户端) | **5,179 QPS** | 2,707 QPS | 2,616 QPS | — | 131 QPS |

# 文档

文档地址：[docs/web](../docs/web/index.md)，API 文档：[zedsearch-docs](https://github.com/zedsearch/zedsearch-docs)

# 截图

## 搜索界面

![Search screen](../docs/hots/search-26.png)

## 索引界面

![Index screen](../docs/hots/index-26.png)

# 入门

## 快速开始

查看 [快速开始](../docs/web/quickstart.md)

# 发布版本

ZedSearch 已有数百个生产环境部署。

各平台的二进制文件见 [Releases 页面](https://github.com/vcaesar/zedsearch/releases/latest)。默认归档文件未使用 UPX 压缩；为了更小的下载体积，所有 Linux 版本和 Windows amd64 版本还额外提供经 [UPX](https://upx.github.io/) 压缩的 `*_upx.tar.gz`（启动略慢，且部分杀毒软件可能会误报压缩过的 `.exe` 文件）。

## 构建

设置 `ZED_FIRST_ADMIN_USER` / `ZED_FIRST_ADMIN_PASSWORD` 或配置 `conf/zed.toml`（见下文），然后在仓库根目录运行：

```sh
go run cmd/zedsearch/main.go      # API-only, :4080
go run cmd/zedsearch/main.go web  # with the web UI (Node.js 24+ and npm)
```

`web` 会生成 `test/zedsearch-web`，一个内嵌 UI 的独立二进制文件。

### 配置

配置按以下顺序生效：内置默认值（[`pkg/config/default.toml`](../pkg/config/default.toml)），然后是工作目录下可选的 `conf/zed.toml`，最后是非空的环境变量。每个键名转为大写即为对应的环境变量，例如 `zed_server_port` → `ZED_SERVER_PORT`。

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

- 只需列出要修改的键；分组的键需保留 `default.toml` 中的表头（`[Shard]`、`[S3]`、`[Etcd]` 等）。
- 未知的键会导致启动失败。时长单位为纳秒，大小单位为字节。
- 首个管理员凭据仅在首次启动时用于创建管理员用户；登录后请修改密码，切勿提交真实凭据。

# 社区

- 如何开发并为 ZedSearch 做贡献

  请查看[贡献指南](../CONTRIBUTING.md)。另请查看[路线图](../docs/web/roadmap.md)

# 示例

你可以使用 ZedSearch 索引和搜索任何数据。以下是一些社区成员使用 zedsearch 索引和搜索 Enron 邮件数据集的示例：

1. https://github.com/jorgeloaiza48/Enron-Email-DataSet
1. https://github.com/jhojanperlaza/email_search_engine
1. https://github.com/carlosarraes/zinmail
1. https://github.com/devjopa/golab-search
1. https://github.com/avaco2312/zincsearch
1. https://github.com/paolorossig/email-indexer
1. https://github.com/ulimonte05/zincsearching
