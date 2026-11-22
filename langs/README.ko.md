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

[English](../README.md) | [简体中文](README.zh.md) | [繁體中文](README.zht.md) | [日本語](README.ja.md) | 한국어 | [Français](README.fr.md) | [Deutsch](README.de.md) | [Español](README.es.md) | [Русский](README.ru.md) | [Português](README.pt.md)

# ZedSearch

ZedSearch는 전문(full text) 색인과 벡터 색인을 수행하는 검색 엔진이자, 로그, 메트릭, 트레이스, 분석, 모니터링, AI를 위한 클라우드 네이티브 관측성(observability) 플랫폼입니다.
Elasticsearch, OpenSearch, Datadog, Splunk의 경량 대안으로, 아주 적은 리소스로 실행됩니다.
기본 색인 라이브러리로 [vcaesar/riot](https://github.com/vcaesar/riot)를 사용합니다.

이해하고 조정해야 할 수십 개의 설정이 필요한 Elasticsearch와 달리 매우 간단하고 운영하기 쉽습니다. ZedSearch는 1분 안에 설치하고 실행할 수 있습니다.

API로 데이터를 수집하고 Kibana로 검색하기만 한다면 Elasticsearch를 그대로 대체할 수 있습니다(ZedSearch는 Kibana를 지원하지 않습니다. ZedSearch는 자체 UI를 제공합니다).

<!-- ZedSearch의 간단한 데모는 아래 동영상을 확인하세요. -->
<!-- [![Zed Youtube](../screenshots/zed-youtube.jpg)](https://www.youtube.com/watch?v=aZXtuVjt1ow) -->

## Homebrew

macOS와 Linux에서는 [vcaesar/homebrew-tap](https://github.com/vcaesar/homebrew-tap)로 설치할 수 있습니다:

```sh
brew install vcaesar/tap/zedsearch
```

## Docker

이미지는 [Docker Hub](https://hub.docker.com/r/riotgo/zedsearch)에 `riotgo/zedsearch`로 게시됩니다(linux/amd64, linux/arm64, linux/arm/v7):

실행하기 전에 환경 변수 `ZED_FIRST_ADMIN_PASSWORD`에 원하는 비밀번호를 설정하세요:

```sh
docker run -d --name zedsearch \
    -p 4080:4080 \
    -v zedsearch-data:/data -e ZED_DATA_PATH=/data \
    -e ZED_FIRST_ADMIN_USER="admin" \
    -e ZED_FIRST_ADMIN_PASSWORD="your_password_zed#123" \
    riotgo/zedsearch:latest
```

이름 있는 볼륨 `zedsearch-data`는 컨테이너 사용자(uid 10001)가 쓸 수 있습니다. 호스트 디렉터리를 쓰려면 `mkdir -p ./data && sudo chown 10001:10001 ./data`를 실행한 뒤 `-v "$PWD/data:/data"`로 마운트하세요.

## 클러스터

노드들은 etcd를 공유하고 인덱스 소유권을 서로 나누며, 복제본과 장애 조치를 지원합니다. 어떤 노드든 모든 요청을 처리할 수 있습니다. 각 노드에는 고유한 `ZED_CLUSTER_NODE_ID`, 다른 노드가 접근할 수 있는 `ZED_CLUSTER_ADVERTISE_ADDR`, 그리고 동일한 `ZED_CLUSTER_SECRET`가 필요합니다. 모델, 설정 및 제한 사항은 [pkg/cluster](../pkg/cluster/README.md)를 참고하세요.

### Docker

```sh
# Use your own admin password and a fresh cluster secret for every deployment.
ZED_PASSWORD='your_password_zed#123'
ZED_SECRET="$(openssl rand -hex 32)"

docker network create zed
docker run -d --name zed-etcd --network zed --restart unless-stopped \
    -v zed-etcd-data:/etcd-data quay.io/coreos/etcd:v3.7.3 etcd \
    --listen-client-urls http://0.0.0.0:2379 --advertise-client-urls http://zed-etcd:2379 \
    --data-dir /etcd-data

# Node to node traffic, with the secret, is plain HTTP on the private network "zed",
# so ports are published on localhost only. Across hosts, advertise https:// addresses
# or use an encrypted network. --restart retries a node that started before etcd.
for i in 1 2 3; do
  docker run -d --name zed$i --network zed -p 127.0.0.1:408$i:4080 --restart unless-stopped \
    -v zed$i-data:/data -e ZED_DATA_PATH=/data \
    -e ZED_FIRST_ADMIN_USER="admin" -e ZED_FIRST_ADMIN_PASSWORD="$ZED_PASSWORD" \
    -e ZED_CLUSTER_ENABLE=true -e ZED_CLUSTER_NAME=prod -e ZED_CLUSTER_NODE_ID=node-$i \
    -e ZED_CLUSTER_ADVERTISE_ADDR=http://zed$i:4080 -e ZED_CLUSTER_SECRET="$ZED_SECRET" \
    -e ZED_ETCD_ENDPOINTS=http://zed-etcd:2379 \
    riotgo/zedsearch:latest
done

curl --retry 10 --retry-connrefused -u admin:"$ZED_PASSWORD" http://localhost:4081/api/cluster/nodes

# Back up the shared metadata in zed-etcd-data; restore a snapshot into a new
# volume with `etcdutl snapshot restore` before starting zed-etcd on it.
docker exec zed-etcd etcdctl snapshot save /etcd-data/snapshot.db
docker cp zed-etcd:/etcd-data/snapshot.db ./etcd-snapshot.db
```

### Kubernetes

[k8s/cluster.yaml](../k8s/cluster.yaml)는 Service `zedsearch` 뒤에서 3노드 StatefulSet `zedsearch-cluster`를 단일 노드 etcd와 함께 실행합니다(프로덕션에서는 멤버 3개를 실행하세요):

```sh
kubectl create namespace zedsearch
kubectl -n zedsearch create secret generic zedsearch-admin \
    --from-literal=ZED_FIRST_ADMIN_USER=admin --from-literal=ZED_FIRST_ADMIN_PASSWORD='your_password_zed#123'
kubectl -n zedsearch create secret generic zedsearch-cluster \
    --from-literal=ZED_CLUSTER_SECRET="$(openssl rand -hex 32)"
kubectl apply -f k8s/cluster.yaml
kubectl -n zedsearch port-forward svc/zedsearch 4080:4080
```

# 왜 ZedSearch인가

Elasticsearch는 매우 좋은 제품이지만, 복잡하고 많은 리소스를 필요로 하며 10년이 넘은 제품이고, 메모리 등 자원을 많이 사용합니다.
ZedSearch는 많은 작업 없이도 누구나 전문 및 벡터 검색 색인, 관측성(observability), 데이터 분석을 더 쉽게 사용할 수 있도록 만들었습니다.

# 기능:

go + gin (`-tags fiber`로 fiber v3 사용 가능) + react

1. 빠른 쿼리와 더 작은 인덱스 파일
2. 전문 색인 및 벡터 색인 기능 제공
3. 설치와 실행을 위한 단일 바이너리. 여러 플랫폼용 바이너리를 Releases에서 제공합니다.
4. React로 작성된 데이터 조회용 Web UI(바이너리에 내장)
5. 데이터 수집을 위한 Elasticsearch API 호환성(단일 레코드 및 벌크 API)
6. 기본 제공 인증
7. 스키마리스 - 스키마를 미리 정의할 필요가 없으며, 같은 인덱스 내의 서로 다른 문서가 서로 다른 필드를 가질 수 있습니다.
8. 디스크, S3, MinIO 등에 인덱스 저장
9. 집계(aggregation) 및 Elasticsearch 기능 전반 지원
10. 다국어 지원, CJK(한중일) 지원
11. 이벤트 시간 정규화와 로그 검색 기본값을 갖추고 JSON, Splunk HEC, Loki, OTLP/HTTP JSON(OpenObserve 호환) 및 Logstash 이벤트를 수집하는 옵트인 [로그 인덱스](../pkg/logs/README.md)
12. kNN 벡터 검색 및 지리 쿼리(`geo_distance`, `geo_bounding_box`, `geo_polygon`, `geo_shape`)
13. Elasticsearch SQL API(`/es/_sql`, `/es/_sql/translate`) 및 MySQL `CREATE TABLE` [스키마를 매핑으로 사용](../docs/sql-mappings.md)
14. 고가용성 [클러스터](../pkg/cluster/README.md): etcd로 조정되는 파티션 소유권, 펜싱 핸드오프, 레플리카, scatter-gather 검색
15. OTLP/HTTP 기반 OpenTelemetry 추적(`zed_tracing_enable`). 모든 요청에 노드 간에 전달되는 `X-Trace-Id` 부여
16. Fleet 및 `zedagent`(`agent/`): 에이전트 등록, 정책 관리, 로그 파일과 시스템 메트릭 전송, 단독 실행도 지원
17. robots를 준수하는 [웹 크롤러](../pkg/crawler/README.md)(`cmd/zedcrawl`), PageRank 계산 및 ZedSearch 벌크 색인 지원

# 벤치마크

문서 50,000개에 대한 검색 애플리케이션 쿼리 7가지(상위 10개와 정확한 전체 개수)입니다. MacBook Pro M1에서 모든 서버를 Docker로 실행했습니다: 필터가 있는 term 쿼리, 두 필드의 OR, 전체 문서의 가격 정렬, 101번째 페이지, terms 및 histogram 집계, 하이라이트. QPS를 제외하면 낮을수록 좋으며, "—"는 해당 엔진이 쿼리를 표현할 수 없음을 뜻합니다(Meilisearch는 숫자 히스토그램이 없고 OpenObserve는 하이라이트를 지원하지 않음).

| 쿼리                        |     ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
| --------------------------- | ------------: | ----------------: | ----------------: | -------------: | ---------------: | --------------: |
| `filtered` (클라이언트 1개) |   **0.89 ms** |           3.43 ms |           1.38 ms |        1.44 ms |          1.87 ms |         9.44 ms |
| `filtered` (클라이언트 8개) | **3,216 QPS** |           471 QPS |         2,742 QPS |      3,066 QPS |        1,924 QPS |         321 QPS |
| `sort_price` (문서 5만 개)  |   **0.74 ms** |           76.0 ms |           2.52 ms |        1.58 ms |          1.09 ms |         4.40 ms |
| `multi_field` title + body  |   **0.63 ms** |           2.04 ms |           1.44 ms |        1.51 ms |          1.19 ms |         21.0 ms |
| `deep_page` from 1000       |   **1.24 ms** |           3.40 ms |           1.42 ms |        1.47 ms |          12.7 ms |         18.1 ms |
| `agg_terms` `category`      |   **1.48 ms** |           5.55 ms |           1.66 ms |        2.12 ms |          1.63 ms |         18.6 ms |
| `agg_histogram` `price`     |   **1.00 ms** |           20.1 ms |           1.67 ms |        1.72 ms |                — |         18.7 ms |
| `highlight` `body`          |   **0.81 ms** |           2.27 ms |           1.96 ms |        1.97 ms |          1.92 ms |               — |

### 단일 쿼리 (v2)

같은 세션에서 실행한 v2 쿼리로, 각각 한 가지 쿼리 유형만 측정합니다. Meilisearch는 OR과 정확한 구문을 표현할 수 없습니다.

| 쿼리                           |     ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
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
| `bool` should (클라이언트 8개) | **5,179 QPS** |         1,292 QPS |         2,707 QPS |      2,616 QPS |                — |         131 QPS |

### 리소스 사용량

Docker 이미지(비압축), 동일한 문서 50,000개의 텍스트 인덱스(`_flush` 후 ZedSearch, Elasticsearch, OpenSearch 모두 약 25 MB), 전체 실행 동안의 메모리 / CPU(100% = vCPU 1개). 데이터는 3회 실행의 중앙값입니다.

| 리소스                     |   ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 |        OpenObserve 1.1 |
| -------------------------- | ----------: | ----------------: | ----------------: | -------------: | ---------------: | ---------------------: |
| Docker 이미지              | **56.4 MB** |           72.4 MB |          1,195 MB |       1,311 MB |           370 MB |                 434 MB |
| 텍스트 인덱스, 문서 5만 개 |     25.9 MB |          133.3 MB |           25.8 MB |        25.7 MB |           454 MB | 14.2 MB (인덱싱 안 됨) |
| 유휴 메모리                |  **14 MiB** |            64 MiB |         2,674 MiB |      2,699 MiB |          179 MiB |                387 MiB |
| 최대 메모리                | **388 MiB** |           858 MiB |         3,185 MiB |      3,191 MiB |        1,014 MiB |                919 MiB |
| 평균 CPU                   |    **134%** |              366% |              157% |           156% |             230% |                   140% |
| 최대 CPU                   |        553% |              710% |              620% |           529% |             545% |               **519%** |

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
