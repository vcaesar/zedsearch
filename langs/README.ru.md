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

[English](../README.md) | [简体中文](README.zh.md) | [繁體中文](README.zht.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Français](README.fr.md) | [Deutsch](README.de.md) | [Español](README.es.md) | Русский | [Português](README.pt.md)

# ZedSearch

ZedSearch — это поисковый движок с полнотекстовой и векторной индексацией, а также облачная (cloud-native) платформа наблюдаемости для логов, метрик, трассировок, аналитики, мониторинга и ИИ.
Это легковесная альтернатива Elasticsearch, OpenSearch, Datadog и Splunk, которой требуется лишь малая доля ресурсов.
В качестве базовой библиотеки индексации используется [vcaesar/riot](https://github.com/vcaesar/riot).

В отличие от Elasticsearch, где нужно разобраться и настроить пару десятков параметров, ZedSearch очень прост в эксплуатации. Его можно запустить за 1 минуту.

Он является прямой заменой Elasticsearch, если вы загружаете и ищете данные только через API. Kibana не поддерживается, ZedSearch предоставляет собственный интерфейс.

<!-- Посмотрите видео ниже, чтобы увидеть краткую демонстрацию ZedSearch. -->
<!-- [![Zed Youtube](../screenshots/zed-youtube.jpg)](https://www.youtube.com/watch?v=aZXtuVjt1ow) -->

## Homebrew

macOS и Linux, через [vcaesar/homebrew-tap](https://github.com/vcaesar/homebrew-tap):

```sh
brew install vcaesar/tap/zedsearch
```

## Docker

Образы опубликованы на [Docker Hub](https://hub.docker.com/r/riotgo/zedsearch) как `riotgo/zedsearch` (linux/amd64, linux/arm64, linux/arm/v7):

Перед запуском задайте выбранный пароль в переменной окружения `ZED_FIRST_ADMIN_PASSWORD`:

```sh
docker run -d --name zedsearch \
    -p 4080:4080 \
    -v zedsearch-data:/data -e ZED_DATA_PATH=/data \
    -e ZED_FIRST_ADMIN_USER="admin" \
    -e ZED_FIRST_ADMIN_PASSWORD="your_password_zed#123" \
    riotgo/zedsearch:latest
```

Именованный том `zedsearch-data` доступен для записи пользователю контейнера (uid 10001); чтобы использовать каталог хоста, выполните `mkdir -p ./data && sudo chown 10001:10001 ./data` и смонтируйте `-v "$PWD/data:/data"`.

## Кластер

Узлы используют общий etcd и распределяют владение индексами между собой, с репликами и переключением при отказе; любой узел обслуживает любой запрос. Каждому узлу нужен уникальный `ZED_CLUSTER_NODE_ID`, доступный другим узлам `ZED_CLUSTER_ADVERTISE_ADDR` и одинаковый `ZED_CLUSTER_SECRET`. Модель, настройки и ограничения описаны в [pkg/cluster](../pkg/cluster/README.md).

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
# so ports are published on localhost only. Across hosts use an encrypted network, or
# advertise https:// addresses and serve them: ZED_SERVER_TLS_CERTIFICATE_FILE and
# ZED_SERVER_TLS_KEY_FILE on every node (or a TLS proxy in front of it), with a
# certificate the other nodes trust (system roots, or a private CA added with
# SSL_CERT_FILE / SSL_CERT_DIR as k8s/cluster.yaml does).
# --restart retries a node that started before etcd.
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

# Back up the shared metadata in zed-etcd-data; the copy runs only after a successful save.
docker exec zed-etcd etcdctl snapshot save /etcd-data/snapshot.db &&
  docker cp zed-etcd:/etcd-data/snapshot.db ./etcd-snapshot.db

# Restore with every node stopped: `etcdutl snapshot restore` into a new volume, then
# empty the in-sync sets before the nodes start, since a replica listed in an old
# snapshot may have missed later writes. Steps: pkg/cluster/README.md, "Backup and restore".
```

### Kubernetes

[k8s/cluster.yaml](../k8s/cluster.yaml) запускает StatefulSet `zedsearch-cluster` из трёх узлов за Service `zedsearch` и etcd из одного узла (в продакшене запускайте три участника):

```sh
# needs cert-manager for the node TLS certificates; nodes serve HTTPS
kubectl create namespace zedsearch
kubectl -n zedsearch create secret generic zedsearch-admin \
    --from-literal=ZED_FIRST_ADMIN_USER=admin --from-literal=ZED_FIRST_ADMIN_PASSWORD='your_password_zed#123'
kubectl -n zedsearch create secret generic zedsearch-cluster \
    --from-literal=ZED_CLUSTER_SECRET="$(openssl rand -hex 32)"
kubectl apply -f k8s/cluster.yaml
kubectl -n zedsearch wait --for=jsonpath='{.status.readyReplicas}'=3 statefulset/zedsearch-cluster --timeout=10m
kubectl -n zedsearch port-forward svc/zedsearch 4080:4080 &
kubectl -n zedsearch get secret zedsearch-tls -o jsonpath='{.data.ca\.crt}' | base64 -d > zedsearch-ca.crt

curl --retry 10 --retry-connrefused --cacert zedsearch-ca.crt -u admin:'your_password_zed#123' https://localhost:4080/api/cluster/nodes
```

# Почему ZedSearch

Elasticsearch и похожие движки — очень хорошие продукты, но они сложны, требуют много памяти и других ресурсов и несут за собой более десяти лет истории проектирования.
Я создал ZedSearch, чтобы полнотекстовый и векторный поиск, наблюдаемость и анализ данных стали проще, быстрее и бесплатными без лишних усилий.

# Возможности

go + gin (или fiber v3 с `-tags fiber`) + react

1. Быстрые запросы и компактные индексные файлы
2. Полнотекстовая и векторная индексация
3. Один бинарный файл для установки и запуска. Бинарные файлы для разных платформ доступны в релизах.
4. Веб-интерфейс для запросов к данным, написанный на React (встроен в бинарный файл)
5. Совместимость с API Elasticsearch для загрузки данных (API одиночной записи и bulk)
6. Встроенная аутентификация
7. Без схемы — не нужно заранее определять схему, а разные документы в одном индексе могут иметь разные поля.
8. Хранение индексов на диске, в S3, MinIO и других хранилищах
9. Агрегации и широкая совместимость с API Elasticsearch с поддержкой полного набора возможностей
10. Многоязычный анализ, включая CJK
11. Опциональные [индексы логов](../pkg/logs/README.md) с нормализацией времени событий и настройками по умолчанию для поиска по логам; приём JSON, Splunk HEC, Loki и OTLP/HTTP JSON (совместимо с OpenObserve), а также событий Logstash
12. Векторный поиск kNN и геозапросы (`geo_distance`, `geo_bounding_box`, `geo_polygon`, `geo_shape`)
13. Гибридный поиск, объединяющий полнотекстовые и kNN-результаты: ретриверы Elasticsearch `rrf` и `linear` (веса, `minmax`/`l2_norm`), устаревший `rank.rrf` и запрос OpenSearch `hybrid` со встроенным `search_pipeline` (`normalization-processor`, `score-ranker-processor` RRF)
14. SQL-запросы через Elasticsearch SQL API (`/es/_sql`, `/api/_sql`, `/es/_sql/translate`): `SELECT … WHERE` (`LIKE`, `IN`, `BETWEEN`, `IS NULL`, `MATCH()`, `QUERY()`), `COUNT`/`SUM`/`AVG`/`MIN`/`MAX`, `GROUP BY` (`HISTOGRAM`, `DATE_HISTOGRAM`), `HAVING`, `ORDER BY`, `LIMIT`, вывод в JSON, txt, csv или tsv; и [схемы MySQL `CREATE TABLE` в качестве маппингов](../docs/sql-mappings.md)
15. Отказоустойчивый [кластер](../pkg/cluster/README.md): владение партициями через etcd, передача с fencing, реплики и scatter-gather поиск
16. Трассировка OpenTelemetry через OTLP/HTTP (`zed_tracing_enable`); каждый запрос получает `X-Trace-Id`, который передаётся между узлами
17. Уровни данных: hot (локальный диск), warm (объектное хранилище) и cold (объектное хранилище только для чтения, общее для всех узлов), переключаются через `/api/index/{index}/_tier` или жизненный цикл индекса (`warm_after`, `cold_after`, `delete_after`) и политики ILM (`/es/_ilm/policy`)
18. Наборы синонимов (`/es/_synonyms`) с перезагрузкой поисковых анализаторов и правила запросов (`/es/_query_rules`), закрепляющие или исключающие документы
19. Переранжирование [Learning to rank](../docs/learning-to-rank.md) линейными, древовидными и XGBoost-моделями (`/es/_ml/trained_models`) на чистом Go
20. Другие API Elasticsearch: `_msearch`, `_count`, `_delete_by_query`, `_reindex`, `_field_caps`, алиасы, шаблоны индексов, `_cat/indices`, `_cat/health` и `_cluster/health`

## Enterprise

1. Межкластерный поиск (CCS): регистрация удалённых кластеров (`/api/cluster/remotes`, `/es/_cluster/settings`, `/es/_remote/info`) и поиск через `/es/<alias>:<index>/_search`
2. Межкластерная репликация (CCR): индексы-последователи и шаблоны автоподписки через API репликации OpenSearch (`/es/_plugins/_replication/{index}/_start`, `_pause`, `_resume`, `_stop`, `_status`, `_autofollow`)
3. Горизонтальное масштабирование одного индекса по [кластеру](../pkg/cluster/README.md#scale-out-of-a-single-index): `settings.cluster_shards` распределяет индекс по шард-индексам со своими владельцами и репликами, маршрутизирует документы по id, выполняет scatter-gather поиск и суммирует `_count` / `_delete_by_query`; `_cluster/health?level=indices|shards` и опциональные частичные результаты поиска
4. Эндпоинты инференса (`/es/_inference`): текстовые эмбеддинги и rerank через OpenAI-совместимые (Ollama, vLLM, LocalAI), Cohere, Jina и Hugging Face TEI сервисы; поля `semantic_text`, запросы `semantic` и `neural`, ретривер `text_similarity_reranker` и обработчик ответа OpenSearch `rerank`
5. Fleet и `zedagent` (`agent/`): регистрация агентов, управление политиками, отправка лог-файлов и системных метрик или автономный режим агента; раздел Fleet в веб-интерфейсе
6. Шардовый [кластерный движок](../pkg/nodes/README.md) в стиле OpenSearch (размещение шардов, кворумная координация, восстановление с пиров, снапшоты, уровни hot/warm) и экспериментальный Raft + gRPC [распределённый узел](../cmd/distribution/README.md)
7. [Веб-краулер](../pkg/crawler/README.md) с учётом robots.txt (`cmd/zedcrawl`), PageRank и bulk-индексацией в ZedSearch

# Бенчмарки

Семь запросов поискового приложения по 50 000 документов (топ-10 и точное общее число), MacBook Pro M1, каждый сервер в Docker: term-запрос с фильтрами, OR по двум полям, сортировка всех документов по цене, 101-я страница, агрегации terms и histogram, подсветка. Меньше — лучше, кроме QPS; «—» означает, что движок не может выразить запрос (у Meilisearch нет числовой гистограммы, OpenObserve не подсвечивает).

| Запрос                            |     ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
| --------------------------------- | ------------: | ----------------: | ----------------: | -------------: | ---------------: | --------------: |
| `filtered` (1 клиент)             |   **0.89 ms** |           3.43 ms |           1.38 ms |        1.44 ms |          1.87 ms |         9.44 ms |
| `filtered` (8 клиентов)           | **3,216 QPS** |           471 QPS |         2,742 QPS |      3,066 QPS |        1,924 QPS |         321 QPS |
| `sort_price` (50 тыс. документов) |   **0.74 ms** |           76.0 ms |           2.52 ms |        1.58 ms |          1.09 ms |         4.40 ms |
| `multi_field` title + body        |   **0.63 ms** |           2.04 ms |           1.44 ms |        1.51 ms |          1.19 ms |         21.0 ms |
| `deep_page` from 1000             |   **1.24 ms** |           3.40 ms |           1.42 ms |        1.47 ms |          12.7 ms |         18.1 ms |
| `agg_terms` `category`            |   **1.48 ms** |           5.55 ms |           1.66 ms |        2.12 ms |          1.63 ms |         18.6 ms |
| `agg_histogram` `price`           |   **1.00 ms** |           20.1 ms |           1.67 ms |        1.72 ms |                — |         18.7 ms |
| `highlight` `body`                |   **0.81 ms** |           2.27 ms |           1.96 ms |        1.97 ms |          1.92 ms |               — |

### Отдельные запросы (v2)

Запросы v2 из тех же сессий, по одному типу запроса. Meilisearch не умеет выражать OR и точные фразы.

| Запрос                     |     ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
| -------------------------- | ------------: | ----------------: | ----------------: | -------------: | ---------------: | --------------: |
| `term` rare                |   **0.50 ms** |           1.03 ms |           1.43 ms |        1.16 ms |          1.14 ms |         15.7 ms |
| `term` mid                 |   **0.58 ms** |           1.82 ms |           1.28 ms |        1.27 ms |          1.16 ms |         17.6 ms |
| `term` keyword             |   **0.57 ms** |           0.82 ms |           1.75 ms |        2.32 ms |          1.03 ms |         5.59 ms |
| `bool` should              |   **0.63 ms** |           1.12 ms |           1.61 ms |        1.75 ms |                — |         26.7 ms |
| `bool` must                |   **1.01 ms** |           1.79 ms |           2.36 ms |        3.30 ms |          1.28 ms |         24.1 ms |
| `range`                    |   **0.64 ms** |           2.56 ms |           1.32 ms |        1.28 ms |          1.32 ms |         5.31 ms |
| `match_all`                |   **0.66 ms** |           4.12 ms |           1.48 ms |        1.38 ms |          1.03 ms |         4.68 ms |
| `prefix` `w1*`             |   **0.71 ms** |          214.5 ms |           1.87 ms |        2.66 ms |          1.32 ms |         11.9 ms |
| `match_phrase`             |   **10.3 ms** |           55.6 ms |           12.7 ms |        12.6 ms |                — |         16.3 ms |
| `bool` should (8 клиентов) | **5,179 QPS** |         1,292 QPS |         2,707 QPS |      2,616 QPS |                — |         131 QPS |

### Потребление ресурсов

Docker-образ (без сжатия), текстовый индекс тех же 50 000 документов (после `_flush` около 25 МБ у ZedSearch, Elasticsearch и OpenSearch), память / CPU за весь прогон (100% = 1 vCPU), данные — медиана трёх прогонов.

| Ресурс                               |   ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 |          OpenObserve 1.1 |
| ------------------------------------ | ----------: | ----------------: | ----------------: | -------------: | ---------------: | -----------------------: |
| Docker-образ                         | **56.4 MB** |           72.4 MB |          1,195 MB |       1,311 MB |           370 MB |                   434 MB |
| Текстовый индекс, 50 тыс. документов |     25.9 MB |          133.3 MB |           25.8 MB |        25.7 MB |           454 MB | 14.2 MB (без индексации) |
| Память в простое                     |  **14 MiB** |            64 MiB |         2,674 MiB |      2,699 MiB |          179 MiB |                  387 MiB |
| Пиковая память                       | **388 MiB** |           858 MiB |         3,185 MiB |      3,191 MiB |        1,014 MiB |                  919 MiB |
| CPU в среднем                        |    **134%** |              366% |              157% |           156% |             230% |                     140% |
| Пиковый CPU                          |        553% |              710% |              620% |           529% |             545% |                 **519%** |

### Векторный поиск

20 000 косинусных векторов размерности 128, k=10, HNSW (M=16, ef_construction 100, 100 кандидатов), по HTTP на той же машине и том же корпусе. Для recall, QPS и загрузки больше — лучше; векторный индекс — прирост данных после загрузки, до `_flush`.

| Метрика                     |        ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | Qdrant 1.20 | Milvus 3.0 | Weaviate 1.40 |
| --------------------------- | ---------------: | ----------------: | -------------: | ---------------: | ----------: | ---------: | ------------: |
| Recall@10                   |            0.939 |         **0.956** |          0.823 |            0.485 |       0.891 |      0.685 |         0.595 |
| Задержка, 1 клиент          |      **1.47 ms** |           2.56 ms |        2.09 ms |          4.71 ms |     1.84 ms |    2.70 ms |       2.28 ms |
| QPS, 8 клиентов             |    **1,923 QPS** |           983 QPS |      1,385 QPS |        1,025 QPS |   1,653 QPS |  1,827 QPS |     1,706 QPS |
| Загрузка + индексация       | **10,070 vec/s** |       3,004 vec/s |    1,770 vec/s |        377 vec/s | 4,609 vec/s |  999 vec/s |   3,103 vec/s |
| Векторный индекс, 20k × 128 |          28.3 MB |           63.9 MB |        75.6 MB |      **25.8 MB** |     41.4 MB |    34.3 MB |       75.6 MB |

### Векторный поиск, кластер из 3 узлов

Медианы трёх запусков: три узла с 1 репликой на одной Docker VM с 8 vCPU, ZedSearch с etcd и репликацией `document`. Узлы делят CPU, поэтому здесь измеряются накладные расходы репликации, а не горизонтальное масштабирование.

| Метрика                                   |     ZedSearch ×3 |     OpenSearch ×3 |
| ----------------------------------------- | ---------------: | ----------------: |
| Recall@10                                 |        **0.960** |             0.905 |
| Задержка, 1 клиент                        |      **1.81 ms** |           3.59 ms |
| QPS, 8 клиентов                           |    **1,611 QPS** |           970 QPS |
| Загрузка + индексация                     |  **4,157 vec/s** |       1,451 vec/s |
| Векторный индекс после `_flush`, все узлы |      **56.5 MB** |           70.4 MB |
| Память в простое / пик, все узлы          | **78 / 902 MiB** | 4,517 / 5,360 MiB |

# Документация

Документация доступна в [docs/web](../docs/web/index.md), документация API — в [zedsearch-docs](https://github.com/zedsearch/zedsearch-docs)

# Скриншоты

## Экран поиска

![Search screen](../docs/hots/search-26.png)

## Экран индексов

![Index screen](../docs/hots/index-26.png)

# Начало работы

## Быстрый старт

См. [Быстрый старт](../docs/web/quickstart.md)

# Релизы

ZedSearch используется в сотнях производственных установок.

Бинарные файлы для всех платформ доступны на [странице релизов](https://github.com/vcaesar/zedsearch/releases/latest). Архивы по умолчанию не сжаты UPX; для меньшего размера загрузки все сборки для Linux и сборка для Windows amd64 также публикуются в виде `*_upx.tar.gz`, упакованного [UPX](https://upx.github.io/) (запуск немного медленнее, а некоторые антивирусы могут ошибочно помечать упакованные `.exe`-файлы).

## Сборка

Задайте `ZED_FIRST_ADMIN_USER` / `ZED_FIRST_ADMIN_PASSWORD` или настройте `conf/zed.toml` (см. ниже) и выполните из корня репозитория:

```sh
go run cmd/zedsearch/main.go      # API-only, :4080
go run cmd/zedsearch/main.go web  # with the web UI (Node.js 24+ and npm)
```

`web` создаёт `test/zedsearch-web` — самостоятельный бинарный файл со встроенным UI.

### Конфигурация

Настройки применяются в таком порядке: встроенные значения по умолчанию ([`pkg/config/default.toml`](../pkg/config/default.toml)), затем необязательный `conf/zed.toml` в рабочем каталоге, затем непустые переменные окружения. Каждый ключ в верхнем регистре — это и переменная окружения, например `zed_server_port` → `ZED_SERVER_PORT`.

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

- Указывайте только изменяемые ключи; сгруппированные ключи сохраняют заголовок таблицы из `default.toml` (`[Shard]`, `[S3]`, `[Etcd]`, ...).
- Неизвестные ключи приводят к ошибке запуска. Длительности задаются в наносекундах, размеры — в байтах.
- Учётные данные первого администратора используются только для создания пользователя admin при первом запуске; смените пароль после входа и никогда не коммитьте реальные учётные данные.

# Сообщество

- Как разрабатывать ZedSearch и вносить вклад

  См. [руководство по участию](../CONTRIBUTING.md). Также см. [дорожную карту](../docs/web/roadmap.md)

# Примеры

ZedSearch можно использовать для индексации и поиска любых данных. В [examples/](../examples) есть скрипты на Python, которые индексируют документ, загружают файлы Word и ищут по ним.

Проекты сообщества, индексирующие и ищущие по датасету писем Enron с помощью ZincSearch (ZedSearch сохраняет его эндпоинты `/api`):

1. https://github.com/jorgeloaiza48/Enron-Email-DataSet
1. https://github.com/jhojanperlaza/email_search_engine
1. https://github.com/carlosarraes/zinmail
1. https://github.com/devjopa/golab-search
1. https://github.com/avaco2312/zincsearch
1. https://github.com/paolorossig/email-indexer
1. https://github.com/ulimonte05/zincsearching
