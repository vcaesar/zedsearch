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

[English](../README.md) | [简体中文](README.zh.md) | [繁體中文](README.zht.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Français](README.fr.md) | [Deutsch](README.de.md) | Español | [Русский](README.ru.md) | [Português](README.pt.md)

# ZedSearch

ZedSearch es un motor de búsqueda que realiza indexación de texto completo y vectorial, y una plataforma cloud-native de observabilidad para logs, métricas, trazas, analítica, monitorización e IA.
Es una alternativa ligera a Elasticsearch, OpenSearch, Datadog y Splunk, y funciona con una fracción de los recursos.
Utiliza [vcaesar/riot](https://github.com/vcaesar/riot) como biblioteca de indexación subyacente.

Es muy simple y fácil de operar, a diferencia de Elasticsearch, que requiere entender y ajustar un par de docenas de parámetros. Puedes tener ZedSearch funcionando en 1 minuto.

Es un reemplazo directo de Elasticsearch si solo ingieres datos mediante APIs y buscas usando Kibana (ZedSearch no es compatible con Kibana. ZedSearch ofrece su propia interfaz).

<!-- Mira el siguiente video para una demostración rápida de ZedSearch. -->
<!-- [![Zed Youtube](../screenshots/zed-youtube.jpg)](https://www.youtube.com/watch?v=aZXtuVjt1ow) -->

## Homebrew

macOS y Linux, mediante [vcaesar/homebrew-tap](https://github.com/vcaesar/homebrew-tap):

```sh
brew install vcaesar/tap/zedsearch
```

## Docker

Las imágenes están en [Docker Hub](https://hub.docker.com/r/riotgo/zedsearch) como `riotgo/zedsearch` (linux/amd64, linux/arm64, linux/arm/v7):

Antes de ejecutar el comando, configure `ZED_FIRST_ADMIN_PASSWORD` en su entorno con la contraseña elegida:

```sh
docker run -d --name zedsearch \
    -p 4080:4080 \
    -v zedsearch-data:/data -e ZED_DATA_PATH=/data \
    -e ZED_FIRST_ADMIN_USER="admin" \
    -e ZED_FIRST_ADMIN_PASSWORD="your_password_zed#123" \
    riotgo/zedsearch:latest
```

El volumen con nombre `zedsearch-data` es escribible por el usuario del contenedor (uid 10001); para usar un directorio del host, ejecute `mkdir -p ./data && sudo chown 10001:10001 ./data` y monte `-v "$PWD/data:/data"`.

## Clúster

Los nodos comparten un etcd y se reparten la propiedad de los índices, con réplicas y conmutación por error; cualquier nodo atiende cualquier petición. Cada nodo necesita un `ZED_CLUSTER_NODE_ID` único, una `ZED_CLUSTER_ADVERTISE_ADDR` accesible desde los demás nodos y el mismo `ZED_CLUSTER_SECRET`. Consulta [pkg/cluster](../pkg/cluster/README.md) para el modelo, la configuración y los límites.

### Docker

```sh
docker network create zed
docker run -d --name zed-etcd --network zed quay.io/coreos/etcd:v3.7.3 etcd \
    --listen-client-urls http://0.0.0.0:2379 --advertise-client-urls http://zed-etcd:2379 \
    --data-dir /etcd-data

for i in 1 2 3; do
  docker run -d --name zed$i --network zed -p 408$i:4080 \
    -v zed$i-data:/data -e ZED_DATA_PATH=/data \
    -e ZED_FIRST_ADMIN_USER="admin" -e ZED_FIRST_ADMIN_PASSWORD="your_password_zed#123" \
    -e ZED_CLUSTER_ENABLE=true -e ZED_CLUSTER_NAME=prod -e ZED_CLUSTER_NODE_ID=node-$i \
    -e ZED_CLUSTER_ADVERTISE_ADDR=http://zed$i:4080 -e ZED_CLUSTER_SECRET="your_cluster_secret_zed#123" \
    -e ZED_ETCD_ENDPOINTS=http://zed-etcd:2379 \
    riotgo/zedsearch:latest
done

curl --retry 10 --retry-connrefused -u admin:"your_password_zed#123" http://localhost:4081/api/cluster/nodes
```

### Kubernetes

[k8s/cluster.yaml](../k8s/cluster.yaml) ejecuta un StatefulSet `zedsearch-cluster` de tres nodos detrás del Service `zedsearch`, con un etcd de un solo nodo (usa tres miembros en producción):

```sh
kubectl create namespace zedsearch
kubectl -n zedsearch create secret generic zedsearch-admin \
    --from-literal=ZED_FIRST_ADMIN_USER=admin --from-literal=ZED_FIRST_ADMIN_PASSWORD='your_password_zed#123'
kubectl -n zedsearch create secret generic zedsearch-cluster --from-literal=ZED_CLUSTER_SECRET='your_cluster_secret_zed#123'
kubectl apply -f k8s/cluster.yaml
kubectl -n zedsearch port-forward svc/zedsearch 4080:4080
```

# Por qué ZedSearch

Aunque Elasticsearch es un producto muy bueno, es complejo, requiere muchos recursos, tiene más de una década y consume mucha memoria, entre otras cosas.
Creé ZedSearch para que sea más fácil usar la indexación de búsqueda de texto completo y vectorial, la observabilidad y el análisis de datos sin mucho trabajo.

# Características:

go + gin (o fiber v3 con `-tags fiber`) + react

1. Consultas rápidas y archivos de índice más pequeños
2. Proporciona capacidad de indexación de texto completo y vectorial
3. Un único binario para la instalación y ejecución. Binarios disponibles en las releases para múltiples plataformas.
4. Interfaz web para consultar datos escrita en React (embebida en el binario)
5. Compatibilidad con las APIs de Elasticsearch para la ingesta de datos (API de registro único y bulk)
6. Autenticación lista para usar
7. Sin esquema: no es necesario definir un esquema por adelantado y diferentes documentos del mismo índice pueden tener campos distintos.
8. Almacenamiento de índices en disco, S3, MinIO y otros
9. Soporte de agregaciones y de las funcionalidades completas de Elasticsearch
10. Soporte multilingüe, incluido CJK
11. [Índices de logs](../pkg/logs/README.md) opcionales con normalización del tiempo del evento y valores predeterminados para búsqueda de logs, con ingesta de JSON, Splunk HEC, Loki y OTLP/HTTP JSON (compatible con OpenObserve) y eventos de Logstash
12. Búsqueda vectorial kNN y consultas geográficas (`geo_distance`, `geo_bounding_box`, `geo_polygon`, `geo_shape`)
13. API SQL de Elasticsearch (`/es/_sql`, `/es/_sql/translate`) y [esquemas MySQL `CREATE TABLE` como mappings](../docs/sql-mappings.md)
14. [Clúster](../pkg/cluster/README.md) de alta disponibilidad: propiedad de particiones coordinada por etcd, traspaso con fencing, réplicas y búsqueda scatter-gather
15. Trazado OpenTelemetry mediante OTLP/HTTP (`zed_tracing_enable`); cada petición recibe un `X-Trace-Id` que la sigue entre nodos
16. Fleet y `zedagent` (`agent/`): registro de agentes, gestión de políticas, envío de archivos de logs y métricas del sistema, o agente independiente
17. [Rastreador web](../pkg/crawler/README.md) que respeta robots.txt (`cmd/zedcrawl`), con PageRank e indexación bulk en ZedSearch

# Benchmarks

Siete consultas de aplicación de búsqueda sobre 50.000 documentos (top 10 más el total exacto), en un MacBook Pro M1, con cada servidor en Docker: una consulta term filtrada, un OR sobre dos campos, un orden por precio sobre todos los documentos, la página 101, una agregación terms y una histogram, y resaltado. Menos es mejor, salvo en QPS; «—» significa que el motor no puede expresar la consulta (Meilisearch no tiene histograma numérico y OpenObserve no resalta).

| Consulta                   |     ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
| -------------------------- | ------------: | ----------------: | ----------------: | -------------: | ---------------: | --------------: |
| `filtered` (1 cliente)     |   **0.89 ms** |           3.43 ms |           1.38 ms |        1.44 ms |          1.87 ms |         9.44 ms |
| `filtered` (8 clientes)    | **3,216 QPS** |           471 QPS |         2,742 QPS |      3,066 QPS |        1,924 QPS |         321 QPS |
| `sort_price` (50k docs)    |   **0.74 ms** |           76.0 ms |           2.52 ms |        1.58 ms |          1.09 ms |         4.40 ms |
| `multi_field` title + body |   **0.63 ms** |           2.04 ms |           1.44 ms |        1.51 ms |          1.19 ms |         21.0 ms |
| `deep_page` from 1000      |   **1.24 ms** |           3.40 ms |           1.42 ms |        1.47 ms |          12.7 ms |         18.1 ms |
| `agg_terms` `category`     |   **1.48 ms** |           5.55 ms |           1.66 ms |        2.12 ms |          1.63 ms |         18.6 ms |
| `agg_histogram` `price`    |   **1.00 ms** |           20.1 ms |           1.67 ms |        1.72 ms |                — |         18.7 ms |
| `highlight` `body`         |   **0.81 ms** |           2.27 ms |           1.96 ms |        1.97 ms |          1.92 ms |               — |

### Consultas simples (v2)

Las consultas v2 de las mismas sesiones, un solo tipo de consulta cada una. Meilisearch no puede expresar OR ni frases exactas.

| Consulta                   |     ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
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
| `bool` should (8 clientes) | **5,179 QPS** |         1,292 QPS |         2,707 QPS |      2,616 QPS |                — |         131 QPS |

### Consumo de recursos

Imagen Docker (sin comprimir), índice de texto de los mismos 50.000 documentos (unos 25 MB para ZedSearch, Elasticsearch y OpenSearch tras `_flush`), y memoria / CPU durante toda la ejecución (100 % = 1 vCPU), los datos son la mediana de tres ejecuciones.

| Recurso                   |   ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 |       OpenObserve 1.1 |
| ------------------------- | ----------: | ----------------: | ----------------: | -------------: | ---------------: | --------------------: |
| Imagen Docker             | **56.4 MB** |           72.4 MB |          1,195 MB |       1,311 MB |           370 MB |                434 MB |
| Índice de texto, 50k docs |     25.9 MB |          133.3 MB |           25.8 MB |        25.7 MB |           454 MB | 14.2 MB (sin indexar) |
| Memoria en reposo         |  **14 MiB** |            64 MiB |         2,674 MiB |      2,699 MiB |          179 MiB |               387 MiB |
| Memoria máxima            | **388 MiB** |           858 MiB |         3,185 MiB |      3,191 MiB |        1,014 MiB |               919 MiB |
| CPU media                 |    **134%** |              366% |              157% |           156% |             230% |                  140% |
| CPU máxima                |        553% |              710% |              620% |           529% |             545% |              **519%** |

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
