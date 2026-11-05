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

[English](../README.md) | [简体中文](README.zh.md) | [繁體中文](README.zht.md) | [日本語](README.ja.md) | 한국어 | [Français](README.fr.md) | [Deutsch](README.de.md) | [Español](README.es.md) | [Русский](README.ru.md) | [Português](README.pt.md)

# ZedSearch

ZedSearch는 전문(full text) 색인을 수행하는 검색 엔진입니다. Elasticsearch의 경량 대안으로, 아주 적은 리소스로 실행됩니다. 기본 색인 라이브러리로 [bluge](https://github.com/blugelabs/bluge)([vcaesar/riot](https://github.com/vcaesar/riot) 포크를 통해)를 사용합니다.

이해하고 조정해야 할 수십 개의 설정이 필요한 Elasticsearch와 달리 매우 간단하고 운영하기 쉽습니다. ZedSearch는 2분 안에 설치하고 실행할 수 있습니다.

API로 데이터를 수집하고 Kibana로 검색하기만 한다면 Elasticsearch를 그대로 대체할 수 있습니다(ZedSearch는 Kibana를 지원하지 않습니다. ZedSearch는 자체 UI를 제공합니다).

ZedSearch의 간단한 데모는 아래 동영상을 확인하세요.

<!-- [![Zed Youtube](../screenshots/zed-youtube.jpg)](https://www.youtube.com/watch?v=aZXtuVjt1ow) -->

# 왜 ZedSearch인가

Elasticsearch는 매우 좋은 제품이지만 복잡하고 많은 리소스가 필요하며 10년도 더 된 제품입니다. 저는 사람들이 많은 작업 없이도 전문 검색 색인을 더 쉽게 사용할 수 있도록 ZedSearch를 만들었습니다.

# 기능:

go + gin + react

1. 전문 색인 기능 제공
2. 설치와 실행을 위한 단일 바이너리. 여러 플랫폼용 바이너리를 Releases에서 제공합니다.
3. React로 작성된 데이터 조회용 Web UI(바이너리에 내장)
4. 데이터 수집을 위한 Elasticsearch API 호환성(단일 레코드 및 벌크 API)
5. 기본 제공 인증
6. 스키마리스 - 스키마를 미리 정의할 필요가 없으며, 같은 인덱스 내의 서로 다른 문서가 서로 다른 필드를 가질 수 있습니다.
7. 인덱스를 디스크에 저장
8. 집계(aggregation) 지원
9. 이벤트 시간 정규화와 로그 검색 기본값을 갖춘 옵트인 [로그 인덱스](../pkg/logs/README.md)

# 벤치마크

문서 50,000개에 대한 검색 애플리케이션 쿼리 7가지(상위 10개와 정확한 전체 개수)입니다. MacBook Pro M1에서 모든 서버를 Docker로 실행했습니다: 필터가 있는 term 쿼리, 두 필드의 OR, 전체 문서의 가격 정렬, 101번째 페이지, terms 및 histogram 집계, 하이라이트. QPS를 제외하면 낮을수록 좋으며, "—"는 해당 엔진이 쿼리를 표현할 수 없음을 뜻합니다(Meilisearch는 숫자 히스토그램이 없고 OpenObserve는 하이라이트를 지원하지 않음).

| 쿼리 | ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
|---|---:|---:|---:|---:|---:|
| `filtered` (클라이언트 1개) | **0.89 ms** | 1.38 ms | 1.44 ms | 1.87 ms | 9.44 ms |
| `filtered` (클라이언트 8개) | 2,959 QPS | 2,742 QPS | **3,066 QPS** | 1,924 QPS | 321 QPS |
| `sort_price` (문서 5만 개) | 1.85 ms | 2.52 ms | 1.58 ms | **1.09 ms** | 4.40 ms |
| `multi_field` title + body | **0.63 ms** | 1.44 ms | 1.51 ms | 1.19 ms | 21.0 ms |
| `deep_page` from 1000 | **1.24 ms** | 1.42 ms | 1.47 ms | 12.7 ms | 18.1 ms |
| `agg_terms` `category` | **1.48 ms** | 1.66 ms | 2.12 ms | 1.63 ms | 18.6 ms |
| `agg_histogram` `price` | **1.00 ms** | 1.67 ms | 1.72 ms | — | 18.7 ms |
| `highlight` `body` | **0.81 ms** | 1.96 ms | 1.97 ms | 1.92 ms | — |

### 단일 쿼리 (v2)

같은 세션에서 실행한 v2 쿼리로, 각각 한 가지 쿼리 유형만 측정합니다. Meilisearch는 OR과 정확한 구문을 표현할 수 없습니다.

| 쿼리 | ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
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
| `bool` should (클라이언트 8개) | **5,179 QPS** | 2,707 QPS | 2,616 QPS | — | 131 QPS |

# 문서

문서는 [docs/web](../docs/web/index.md)에서, API 문서는 [zedsearch-docs](https://github.com/zedsearch/zedsearch-docs)에서 확인할 수 있습니다

# 스크린샷

## 검색 화면

![Search screen](../docs/hots/search-26.png)

## 인덱스 화면

![Index screen](../docs/hots/index-26.png)

# 시작하기

## 빠른 시작

[빠른 시작](../docs/web/quickstart.md)을 확인하세요

# 릴리스

ZedSearch는 수백 개의 프로덕션 환경에 설치되어 있습니다.

모든 플랫폼용 바이너리는 [릴리스 페이지](https://github.com/vcaesar/zedsearch/releases/latest)에서 받을 수 있습니다. 기본 아카이브는 UPX로 압축되지 않습니다. 더 작은 다운로드가 필요하면 모든 Linux 빌드와 Windows amd64 빌드에 [UPX](https://upx.github.io/)로 압축한 `*_upx.tar.gz`도 제공됩니다(시작이 약간 느려지며, 일부 백신 프로그램이 압축된 `.exe` 파일을 오탐할 수 있습니다).

## 빌드

`ZED_FIRST_ADMIN_USER` / `ZED_FIRST_ADMIN_PASSWORD`를 설정하거나 `conf/zed.toml`을 구성한 뒤(아래 참조) 저장소 루트에서 실행합니다:

```sh
go run cmd/zedsearch/main.go      # API-only, :4080
go run cmd/zedsearch/main.go web  # with the web UI (Node.js 24+ and npm)
```

`web`은 UI가 내장된 독립 실행형 바이너리 `test/zedsearch-web`를 생성합니다.

### 설정

설정은 다음 순서로 적용됩니다: 내장 기본값([`pkg/config/default.toml`](../pkg/config/default.toml)), 작업 디렉터리의 선택적 `conf/zed.toml`, 비어 있지 않은 환경 변수. 각 키를 대문자로 바꾼 이름이 환경 변수입니다(예: `zed_server_port` → `ZED_SERVER_PORT`).

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

- 변경할 키만 작성하세요. 그룹화된 키는 `default.toml`의 테이블 헤더(`[Shard]`, `[S3]`, `[Etcd]` 등)를 유지해야 합니다.
- 알 수 없는 키가 있으면 시작에 실패합니다. 기간은 나노초, 크기는 바이트 단위입니다.
- 초기 관리자 자격 증명은 첫 시작 시 관리자 사용자 생성에만 사용됩니다. 로그인 후 비밀번호를 변경하고 실제 자격 증명은 커밋하지 마세요.

# 커뮤니티

- ZedSearch 개발 및 기여 방법

  [기여 가이드](../CONTRIBUTING.md)를 확인하세요. [로드맵](../docs/web/roadmap.md)도 확인하세요

# 예제

ZedSearch를 사용하여 어떤 데이터든 색인하고 검색할 수 있습니다. 다음은 사람들이 zedsearch를 사용해 Enron 이메일 데이터셋을 색인하고 검색한 예제입니다:

1. https://github.com/jorgeloaiza48/Enron-Email-DataSet
1. https://github.com/jhojanperlaza/email_search_engine
1. https://github.com/carlosarraes/zinmail
1. https://github.com/devjopa/golab-search
1. https://github.com/avaco2312/zincsearch
1. https://github.com/paolorossig/email-indexer
1. https://github.com/ulimonte05/zincsearching
