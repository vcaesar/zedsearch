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

[English](../README.md) | [简体中文](README.zh.md) | [繁體中文](README.zht.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Français](README.fr.md) | [Deutsch](README.de.md) | [Español](README.es.md) | Русский | [Português](README.pt.md)

# ZedSearch

ZedSearch — это поисковый движок с полнотекстовой индексацией. Это легковесная альтернатива Elasticsearch, которой требуется лишь малая доля ресурсов. В качестве базовой библиотеки индексации используется [bluge](https://github.com/blugelabs/bluge) (через форк [vcaesar/riot](https://github.com/vcaesar/riot)).

В отличие от Elasticsearch, где нужно разобраться и настроить пару десятков параметров, ZedSearch очень прост в эксплуатации. Его можно запустить за 2 минуты.

Он является прямой заменой Elasticsearch, если вы только загружаете данные через API и ищете с помощью Kibana (Kibana не поддерживается ZedSearch. ZedSearch предоставляет собственный интерфейс).

Посмотрите видео ниже, чтобы увидеть краткую демонстрацию ZedSearch.

<!-- [![Zed Youtube](../screenshots/zed-youtube.jpg)](https://www.youtube.com/watch?v=aZXtuVjt1ow) -->

# Почему ZedSearch

Хотя Elasticsearch — очень хороший продукт, он сложен, требует много ресурсов и существует уже более десяти лет. Я создал ZedSearch, чтобы людям было проще пользоваться полнотекстовой поисковой индексацией без лишней работы.

# Возможности:

go + gin + react

1. Полнотекстовая индексация
2. Один бинарный файл для установки и запуска. Бинарные файлы для разных платформ доступны в релизах.
3. Веб-интерфейс для запросов к данным, написанный на React (встроен в бинарный файл)
4. Совместимость с API Elasticsearch для загрузки данных (API одиночной записи и bulk)
5. Встроенная аутентификация
6. Без схемы — не нужно заранее определять схему, а разные документы в одном индексе могут иметь разные поля.
7. Хранение индексов на диске
8. Поддержка агрегаций
9. Опциональные [индексы логов](../pkg/logs/README.md) с нормализацией времени событий и настройками по умолчанию для поиска по логам

# Бенчмарки

Семь запросов поискового приложения по 50 000 документов (топ-10 и точное общее число), MacBook Pro M1, каждый сервер в Docker: term-запрос с фильтрами, OR по двум полям, сортировка всех документов по цене, 101-я страница, агрегации terms и histogram, подсветка. Меньше — лучше, кроме QPS; «—» означает, что движок не может выразить запрос (у Meilisearch нет числовой гистограммы, OpenObserve не подсвечивает).

| Запрос | ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
|---|---:|---:|---:|---:|---:|
| `filtered` (1 клиент) | **0.89 ms** | 1.38 ms | 1.44 ms | 1.87 ms | 9.44 ms |
| `filtered` (8 клиентов) | 2,959 QPS | 2,742 QPS | **3,066 QPS** | 1,924 QPS | 321 QPS |
| `sort_price` (50 тыс. документов) | 1.85 ms | 2.52 ms | 1.58 ms | **1.09 ms** | 4.40 ms |
| `multi_field` title + body | **0.63 ms** | 1.44 ms | 1.51 ms | 1.19 ms | 21.0 ms |
| `deep_page` from 1000 | **1.24 ms** | 1.42 ms | 1.47 ms | 12.7 ms | 18.1 ms |
| `agg_terms` `category` | **1.48 ms** | 1.66 ms | 2.12 ms | 1.63 ms | 18.6 ms |
| `agg_histogram` `price` | **1.00 ms** | 1.67 ms | 1.72 ms | — | 18.7 ms |
| `highlight` `body` | **0.81 ms** | 1.96 ms | 1.97 ms | 1.92 ms | — |

### Отдельные запросы (v2)

Запросы v2 из тех же сессий, по одному типу запроса. Meilisearch не умеет выражать OR и точные фразы.

| Запрос | ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
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
| `bool` should (8 клиентов) | **5,179 QPS** | 2,707 QPS | 2,616 QPS | — | 131 QPS |

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

ZedSearch можно использовать для индексации и поиска любых данных. Вот несколько примеров индексации и поиска по датасету писем Enron с помощью zedsearch:

1. https://github.com/jorgeloaiza48/Enron-Email-DataSet
1. https://github.com/jhojanperlaza/email_search_engine
1. https://github.com/carlosarraes/zinmail
1. https://github.com/devjopa/golab-search
1. https://github.com/avaco2312/zincsearch
1. https://github.com/paolorossig/email-indexer
1. https://github.com/ulimonte05/zincsearching
