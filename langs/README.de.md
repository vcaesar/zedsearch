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

[English](../README.md) | [简体中文](README.zh.md) | [繁體中文](README.zht.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Français](README.fr.md) | Deutsch | [Español](README.es.md) | [Русский](README.ru.md) | [Português](README.pt.md)

# ZedSearch

ZedSearch ist eine Suchmaschine für Volltextindizierung. Sie ist eine leichtgewichtige Alternative zu Elasticsearch und benötigt nur einen Bruchteil der Ressourcen. Als zugrunde liegende Indizierungsbibliothek wird [bluge](https://github.com/blugelabs/bluge) (über den Fork [vcaesar/riot](https://github.com/vcaesar/riot)) verwendet.

Im Gegensatz zu Elasticsearch, bei dem man ein paar Dutzend Stellschrauben verstehen und abstimmen muss, ist sie sehr einfach zu betreiben. Sie können ZedSearch in 2 Minuten zum Laufen bringen.

Sie ist ein direkter Ersatz für Elasticsearch, wenn Sie nur Daten über APIs einspielen und mit Kibana suchen (Kibana wird von ZedSearch nicht unterstützt. ZedSearch bietet eine eigene Oberfläche).

Im folgenden Video sehen Sie eine kurze Demo von ZedSearch.

<!-- [![Zed Youtube](../screenshots/zed-youtube.jpg)](https://www.youtube.com/watch?v=aZXtuVjt1ow) -->

# Warum ZedSearch

Elasticsearch ist zwar ein sehr gutes Produkt, aber es ist komplex, benötigt viele Ressourcen und ist über zehn Jahre alt. Ich habe ZedSearch entwickelt, damit Volltext-Suchindizierung ohne großen Aufwand einfacher nutzbar wird.

# Funktionen:

go + gin + react

1. Bietet Volltextindizierung
2. Eine einzige Binärdatei für Installation und Betrieb. Binärdateien für mehrere Plattformen sind unter Releases verfügbar.
3. In React geschriebene Web-Oberfläche zum Abfragen von Daten (in die Binärdatei eingebettet)
4. Kompatibilität mit Elasticsearch-APIs für die Datenaufnahme (Einzeldatensatz- und Bulk-API)
5. Authentifizierung ab Werk
6. Schemalos – kein Schema muss vorab definiert werden, und verschiedene Dokumente im selben Index können unterschiedliche Felder haben.
7. Indexspeicherung auf der Festplatte
8. Unterstützung für Aggregationen
9. Optionale [Log-Indizes](../pkg/logs/README.md) mit Normalisierung der Ereigniszeit und Standardeinstellungen für die Log-Suche

# Benchmarks

Sieben Suchanwendungs-Abfragen über 50.000 Dokumente (Top 10 plus exakte Gesamtzahl) auf einem MacBook Pro M1, jeder Server in Docker: eine gefilterte Term-Abfrage, ein OR über zwei Felder, eine Sortierung aller Dokumente nach Preis, Seite 101, eine Terms- und eine Histogram-Aggregation sowie Hervorhebung. Niedriger ist besser, außer bei QPS; „—“ heißt, dass die Engine die Abfrage nicht ausdrücken kann (Meilisearch hat kein numerisches Histogramm, OpenObserve keine Hervorhebung).

| Abfrage | ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
|---|---:|---:|---:|---:|---:|
| `filtered` (1 Client) | **0.89 ms** | 1.38 ms | 1.44 ms | 1.87 ms | 9.44 ms |
| `filtered` (8 Clients) | **3,216 QPS** | 2,742 QPS | 3,066 QPS | 1,924 QPS | 321 QPS |
| `sort_price` (50k Dokumente) | **0.74 ms** | 2.52 ms | 1.58 ms | 1.09 ms | 4.40 ms |
| `multi_field` title + body | **0.63 ms** | 1.44 ms | 1.51 ms | 1.19 ms | 21.0 ms |
| `deep_page` from 1000 | **1.24 ms** | 1.42 ms | 1.47 ms | 12.7 ms | 18.1 ms |
| `agg_terms` `category` | **1.48 ms** | 1.66 ms | 2.12 ms | 1.63 ms | 18.6 ms |
| `agg_histogram` `price` | **1.00 ms** | 1.67 ms | 1.72 ms | — | 18.7 ms |
| `highlight` `body` | **0.81 ms** | 1.96 ms | 1.97 ms | 1.92 ms | — |

### Einzelne Abfragen (v2)

Die v2-Abfragen aus denselben Sitzungen, jeweils nur ein Abfragetyp. Meilisearch kann weder OR noch exakte Phrasen ausdrücken.

| Abfrage | ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
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
| `bool` should (8 Clients) | **5,179 QPS** | 2,707 QPS | 2,616 QPS | — | 131 QPS |

# Dokumentation

Die Dokumentation ist unter [docs/web](../docs/web/index.md) verfügbar, die API-Dokumentation unter [zedsearch-docs](https://github.com/zedsearch/zedsearch-docs)

# Screenshots

## Suchansicht

![Search screen](../docs/hots/search-26.png)

## Indexansicht

![Index screen](../docs/hots/index-26.png)

# Erste Schritte

## Schnellstart

Siehe [Schnellstart](../docs/web/quickstart.md)

# Releases

ZedSearch hat Hunderte von Produktionsinstallationen.

Binärdateien für alle Plattformen finden Sie auf der [Release-Seite](https://github.com/vcaesar/zedsearch/releases/latest). Die Standardarchive sind nicht mit UPX komprimiert; für einen kleineren Download gibt es für alle Linux-Builds und den Windows-amd64-Build zusätzlich ein mit [UPX](https://upx.github.io/) gepacktes `*_upx.tar.gz` (etwas langsamerer Start, und manche Antivirenprogramme melden gepackte `.exe`-Dateien möglicherweise fälschlich).

## Build

Setzen Sie `ZED_FIRST_ADMIN_USER` / `ZED_FIRST_ADMIN_PASSWORD` oder konfigurieren Sie `conf/zed.toml` (siehe unten) und führen Sie im Repository-Stammverzeichnis aus:

```sh
go run cmd/zedsearch/main.go      # API-only, :4080
go run cmd/zedsearch/main.go web  # with the web UI (Node.js 24+ and npm)
```

`web` erzeugt `test/zedsearch-web`, ein eigenständiges Binary mit eingebetteter UI.

### Konfiguration

Einstellungen werden in dieser Reihenfolge angewendet: eingebaute Standardwerte ([`pkg/config/default.toml`](../pkg/config/default.toml)), dann die optionale `conf/zed.toml` im Arbeitsverzeichnis, dann nicht leere Umgebungsvariablen. Jeder Schlüssel ist in Großbuchstaben auch eine Umgebungsvariable, z. B. `zed_server_port` → `ZED_SERVER_PORT`.

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

- Tragen Sie nur die Schlüssel ein, die Sie ändern; gruppierte Schlüssel behalten ihren Tabellen-Header aus `default.toml` (`[Shard]`, `[S3]`, `[Etcd]`, ...).
- Unbekannte Schlüssel lassen den Start fehlschlagen. Zeitdauern sind in Nanosekunden, Größen in Bytes angegeben.
- Die Zugangsdaten des ersten Administrators legen nur beim ersten Start den Admin-Benutzer an; ändern Sie das Passwort nach der Anmeldung und committen Sie nie echte Zugangsdaten.

# Community

- Wie man ZedSearch entwickelt und dazu beiträgt

  Siehe den [Leitfaden für Beiträge](../CONTRIBUTING.md). Siehe auch die [Roadmap](../docs/web/roadmap.md)

# Beispiele

Mit ZedSearch können Sie beliebige Daten indizieren und durchsuchen. Hier sind einige Beispiele, mit denen Nutzer den Enron-E-Mail-Datensatz mit zedsearch indiziert und durchsucht haben:

1. https://github.com/jorgeloaiza48/Enron-Email-DataSet
1. https://github.com/jhojanperlaza/email_search_engine
1. https://github.com/carlosarraes/zinmail
1. https://github.com/devjopa/golab-search
1. https://github.com/avaco2312/zincsearch
1. https://github.com/paolorossig/email-indexer
1. https://github.com/ulimonte05/zincsearching
