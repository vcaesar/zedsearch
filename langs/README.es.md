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

[English](../README.md) | [简体中文](README.zh.md) | [繁體中文](README.zht.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Français](README.fr.md) | [Deutsch](README.de.md) | Español | [Русский](README.ru.md) | [Português](README.pt.md)

# ZedSearch

ZedSearch es un motor de búsqueda que realiza indexación de texto completo. Es una alternativa ligera a Elasticsearch y funciona con una fracción de los recursos. Utiliza [bluge](https://github.com/blugelabs/bluge) (a través del fork [vcaesar/riot](https://github.com/vcaesar/riot)) como biblioteca de indexación subyacente.

Es muy simple y fácil de operar, a diferencia de Elasticsearch, que requiere entender y ajustar un par de docenas de parámetros. Puedes tener ZedSearch funcionando en 2 minutos.

Es un reemplazo directo de Elasticsearch si solo ingieres datos mediante APIs y buscas usando Kibana (ZedSearch no es compatible con Kibana. ZedSearch ofrece su propia interfaz).

Mira el siguiente video para una demostración rápida de ZedSearch.

<!-- [![Zed Youtube](../screenshots/zed-youtube.jpg)](https://www.youtube.com/watch?v=aZXtuVjt1ow) -->

# Por qué ZedSearch

Aunque Elasticsearch es un producto muy bueno, es complejo, requiere muchos recursos y tiene más de una década. Construí ZedSearch para que a la gente le resulte más fácil usar la indexación de búsqueda de texto completo sin mucho trabajo.

# Características:

go + gin + react

1. Proporciona capacidad de indexación de texto completo
2. Un único binario para la instalación y ejecución. Binarios disponibles en las releases para múltiples plataformas.
3. Interfaz web para consultar datos escrita en React (embebida en el binario)
4. Compatibilidad con las APIs de Elasticsearch para la ingesta de datos (API de registro único y bulk)
5. Autenticación lista para usar
6. Sin esquema: no es necesario definir un esquema por adelantado y diferentes documentos del mismo índice pueden tener campos distintos.
7. Almacenamiento de índices en disco
8. Soporte de agregaciones
9. [Índices de logs](../pkg/logs/README.md) opcionales con normalización del tiempo del evento y valores predeterminados para búsqueda de logs

# Benchmarks

Siete consultas de aplicación de búsqueda sobre 50.000 documentos (top 10 más el total exacto), en un MacBook Pro M1, con cada servidor en Docker: una consulta term filtrada, un OR sobre dos campos, un orden por precio sobre todos los documentos, la página 101, una agregación terms y una histogram, y resaltado. Menos es mejor, salvo en QPS; «—» significa que el motor no puede expresar la consulta (Meilisearch no tiene histograma numérico y OpenObserve no resalta).

| Consulta | ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
|---|---:|---:|---:|---:|---:|
| `filtered` (1 cliente) | **0.89 ms** | 1.38 ms | 1.44 ms | 1.87 ms | 9.44 ms |
| `filtered` (8 clientes) | **3,216 QPS** | 2,742 QPS | 3,066 QPS | 1,924 QPS | 321 QPS |
| `sort_price` (50k docs) | **0.74 ms** | 2.52 ms | 1.58 ms | 1.09 ms | 4.40 ms |
| `multi_field` title + body | **0.63 ms** | 1.44 ms | 1.51 ms | 1.19 ms | 21.0 ms |
| `deep_page` from 1000 | **1.24 ms** | 1.42 ms | 1.47 ms | 12.7 ms | 18.1 ms |
| `agg_terms` `category` | **1.48 ms** | 1.66 ms | 2.12 ms | 1.63 ms | 18.6 ms |
| `agg_histogram` `price` | **1.00 ms** | 1.67 ms | 1.72 ms | — | 18.7 ms |
| `highlight` `body` | **0.81 ms** | 1.96 ms | 1.97 ms | 1.92 ms | — |

### Consultas simples (v2)

Las consultas v2 de las mismas sesiones, un solo tipo de consulta cada una. Meilisearch no puede expresar OR ni frases exactas.

| Consulta | ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
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
| `bool` should (8 clientes) | **5,179 QPS** | 2,707 QPS | 2,616 QPS | — | 131 QPS |

# Documentación

La documentación está disponible en [docs/web](../docs/web/index.md) y la documentación de la API en [zedsearch-docs](https://github.com/zedsearch/zedsearch-docs)

# Capturas de pantalla

## Pantalla de búsqueda

![Search screen](../docs/hots/search-26.png)

## Pantalla de índices

![Index screen](../docs/hots/index-26.png)

# Primeros pasos

## Inicio rápido

Consulta el [inicio rápido](../docs/web/quickstart.md)

# Versiones

ZedSearch tiene cientos de instalaciones en producción.

Los binarios para todas las plataformas están en la [página de releases](https://github.com/vcaesar/zedsearch/releases/latest). Los archivos predeterminados no están comprimidos con UPX; para una descarga más pequeña, todas las compilaciones de Linux y la de Windows amd64 también se publican como `*_upx.tar.gz` empaquetado con [UPX](https://upx.github.io/) (arranque algo más lento, y algunos antivirus pueden marcar los archivos `.exe` empaquetados).

## Compilación

Define `ZED_FIRST_ADMIN_USER` / `ZED_FIRST_ADMIN_PASSWORD` o configura `conf/zed.toml` (ver abajo) y ejecuta desde la raíz del repositorio:

```sh
go run cmd/zedsearch/main.go      # API-only, :4080
go run cmd/zedsearch/main.go web  # with the web UI (Node.js 24+ and npm)
```

`web` genera `test/zedsearch-web`, un binario independiente con la interfaz integrada.

### Configuración

La configuración se aplica en este orden: valores predeterminados integrados ([`pkg/config/default.toml`](../pkg/config/default.toml)), luego el archivo opcional `conf/zed.toml` del directorio de trabajo y, por último, las variables de entorno no vacías. Cada clave en mayúsculas es también una variable de entorno, p. ej. `zed_server_port` → `ZED_SERVER_PORT`.

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

- Incluye solo las claves que cambies; las claves agrupadas mantienen su encabezado de tabla de `default.toml` (`[Shard]`, `[S3]`, `[Etcd]`, ...).
- Las claves desconocidas hacen fallar el arranque. Las duraciones se expresan en nanosegundos y los tamaños en bytes.
- Las credenciales del primer administrador solo crean el usuario admin en el primer arranque; cambia la contraseña tras iniciar sesión y nunca hagas commit de credenciales reales.

# Comunidad

- Cómo desarrollar y contribuir a ZedSearch

  Consulta la [guía de contribución](../CONTRIBUTING.md). Consulta también la [hoja de ruta](../docs/web/roadmap.md)

# Ejemplos

Puedes usar ZedSearch para indexar y buscar cualquier dato. Estos son algunos ejemplos que la comunidad ha creado para indexar y buscar el conjunto de datos de correos de Enron usando zedsearch:

1. https://github.com/jorgeloaiza48/Enron-Email-DataSet
1. https://github.com/jhojanperlaza/email_search_engine
1. https://github.com/carlosarraes/zinmail
1. https://github.com/devjopa/golab-search
1. https://github.com/avaco2312/zincsearch
1. https://github.com/paolorossig/email-indexer
1. https://github.com/ulimonte05/zincsearching
