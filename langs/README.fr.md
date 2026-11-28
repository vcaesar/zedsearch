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

[English](../README.md) | [简体中文](README.zh.md) | [繁體中文](README.zht.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | Français | [Deutsch](README.de.md) | [Español](README.es.md) | [Русский](README.ru.md) | [Português](README.pt.md)

# ZedSearch

ZedSearch est un moteur de recherche qui effectue de l'indexation plein texte et vectorielle, ainsi qu'une plateforme cloud-native d'observabilité pour les logs, les métriques, les traces, l'analytique, la supervision et l'IA.
C'est une alternative légère à Elasticsearch, OpenSearch, Datadog et Splunk qui fonctionne avec une fraction des ressources.
Il utilise [vcaesar/riot](https://github.com/vcaesar/riot) comme bibliothèque d'indexation sous-jacente.

Il est très simple et facile à exploiter, contrairement à Elasticsearch qui nécessite de comprendre et d'ajuster une vingtaine de paramètres. Vous pouvez démarrer ZedSearch en 1 minute.

C'est un remplacement direct d'Elasticsearch si vous ingérez et recherchez des données uniquement via ses API. Kibana n'est pas pris en charge, ZedSearch fournit sa propre interface.

<!-- Regardez la vidéo ci-dessous pour une démonstration rapide de ZedSearch. -->
<!-- [![Zed Youtube](../screenshots/zed-youtube.jpg)](https://www.youtube.com/watch?v=aZXtuVjt1ow) -->

## Homebrew

macOS et Linux, via [vcaesar/homebrew-tap](https://github.com/vcaesar/homebrew-tap) :

```sh
brew install vcaesar/tap/zedsearch
```

## Docker

Les images sont publiées sur [Docker Hub](https://hub.docker.com/r/riotgo/zedsearch) sous `riotgo/zedsearch` (linux/amd64, linux/arm64, linux/arm/v7) :

Avant de lancer la commande, définissez `ZED_FIRST_ADMIN_PASSWORD` dans votre environnement avec le mot de passe choisi :

```sh
docker run -d --name zedsearch \
    -p 4080:4080 \
    -v zedsearch-data:/data -e ZED_DATA_PATH=/data \
    -e ZED_FIRST_ADMIN_USER="admin" \
    -e ZED_FIRST_ADMIN_PASSWORD="your_password_zed#123" \
    riotgo/zedsearch:latest
```

Le volume nommé `zedsearch-data` est accessible en écriture à l'utilisateur du conteneur (uid 10001) ; pour utiliser un répertoire de l'hôte, lancez `mkdir -p ./data && sudo chown 10001:10001 ./data` puis montez `-v "$PWD/data:/data"`.

## Cluster

Les nœuds partagent un etcd et se répartissent la propriété des index, avec réplicas et basculement ; n'importe quel nœud sert n'importe quelle requête. Chaque nœud a besoin d'un `ZED_CLUSTER_NODE_ID` unique, d'une `ZED_CLUSTER_ADVERTISE_ADDR` joignable par les autres nœuds et du même `ZED_CLUSTER_SECRET`. Voir [pkg/cluster](../pkg/cluster/README.md) pour le modèle, les paramètres et les limites.

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

[k8s/cluster.yaml](../k8s/cluster.yaml) lance un StatefulSet `zedsearch-cluster` de trois nœuds derrière le Service `zedsearch`, avec un etcd à un seul nœud (utilisez trois membres en production) :

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

# Pourquoi ZedSearch

Elasticsearch et les moteurs similaires sont de très bons produits, mais ils sont complexes, demandent beaucoup de mémoire et d'autres ressources, et portent plus de dix ans d'historique de conception.
J'ai créé ZedSearch pour que la recherche plein texte et vectorielle, l'observabilité et l'analyse de données deviennent plus simples, plus rapides et gratuites, sans trop d'efforts.

# Fonctionnalités

go + gin (ou fiber v3 avec `-tags fiber`) + react

1. Requêtes rapides et fichiers d'index plus petits
2. Fournit une capacité d'indexation plein texte et vectorielle
3. Un seul binaire pour l'installation et l'exécution. Binaires disponibles dans les releases pour plusieurs plateformes.
4. Interface web de requêtage des données écrite en React (intégrée au binaire)
5. Compatibilité avec les API Elasticsearch pour l'ingestion de données (API d'enregistrement unique et bulk)
6. Authentification prête à l'emploi
7. Sans schéma - inutile de définir un schéma à l'avance, et différents documents d'un même index peuvent avoir des champs différents.
8. Stockage des index sur disque, S3, MinIO et autres
9. Agrégations et large compatibilité avec l'API Elasticsearch, avec prise en charge complète des fonctionnalités
10. Analyse multilingue, y compris CJK
11. [Index de logs](../pkg/logs/README.md) optionnels avec normalisation de l'horodatage des événements et paramètres par défaut pour la recherche de logs, ingérant JSON, Splunk HEC, Loki et OTLP/HTTP JSON (compatible OpenObserve) ainsi que les événements Logstash
12. Recherche vectorielle kNN et requêtes géographiques (`geo_distance`, `geo_bounding_box`, `geo_polygon`, `geo_shape`)
13. Recherche hybride fusionnant les résultats plein texte et kNN : retrievers Elasticsearch `rrf` et `linear` (poids, `minmax`/`l2_norm`), l'ancien `rank.rrf` et la requête OpenSearch `hybrid` avec une `search_pipeline` en ligne (`normalization-processor`, `score-ranker-processor` RRF)
14. Requêtes SQL via l'API SQL Elasticsearch (`/es/_sql`, `/api/_sql`, `/es/_sql/translate`) : `SELECT … WHERE` (`LIKE`, `IN`, `BETWEEN`, `IS NULL`, `MATCH()`, `QUERY()`), `COUNT`/`SUM`/`AVG`/`MIN`/`MAX`, `GROUP BY` (`HISTOGRAM`, `DATE_HISTOGRAM`), `HAVING`, `ORDER BY`, `LIMIT`, sortie JSON, txt, csv ou tsv ; et [schémas MySQL `CREATE TABLE` comme mappings](../docs/sql-mappings.md)
15. [Cluster](../pkg/cluster/README.md) haute disponibilité : propriété des partitions coordonnée par etcd, transfert avec fencing, réplicas et recherche scatter-gather
16. Traçage OpenTelemetry via OTLP/HTTP (`zed_tracing_enable`) ; chaque requête reçoit un `X-Trace-Id` qui la suit d'un nœud à l'autre
17. Niveaux de données : hot (disque local), warm (stockage objet) et cold (stockage objet en lecture seule partagé par tous les nœuds), changés via `/api/index/{index}/_tier` ou par le cycle de vie de l'index (`warm_after`, `cold_after`, `delete_after`) et les politiques ILM (`/es/_ilm/policy`)
18. Ensembles de synonymes (`/es/_synonyms`) avec rechargement des analyseurs de recherche, et règles de requête (`/es/_query_rules`) pour épingler ou exclure des documents
19. Rescoring [Learning to rank](../docs/learning-to-rank.md) avec des modèles linéaires, arbres et XGBoost (`/es/_ml/trained_models`), évalués en Go pur
20. Plus d'API Elasticsearch : `_msearch`, `_count`, `_delete_by_query`, `_reindex`, `_field_caps`, alias, modèles d'index, `_cat/indices`, `_cat/health` et `_cluster/health`

## Entreprise

1. Recherche inter-clusters (CCS) : enregistrement de clusters distants (`/api/cluster/remotes`, `/es/_cluster/settings`, `/es/_remote/info`) et recherche avec `/es/<alias>:<index>/_search`
2. Réplication inter-clusters (CCR) : index suiveurs et motifs de suivi automatique via l'API de réplication OpenSearch (`/es/_plugins/_replication/{index}/_start`, `_pause`, `_resume`, `_stop`, `_status`, `_autofollow`)
3. Montée en charge d'un index unique sur le [cluster](../pkg/cluster/README.md#scale-out-of-a-single-index) : `settings.cluster_shards` répartit un index sur des index de shards ayant chacun leur propriétaire et leurs réplicas, route les documents par id, effectue des recherches scatter-gather et additionne `_count` / `_delete_by_query` ; `_cluster/health?level=indices|shards` et résultats de recherche partiels en option
4. Points de terminaison d'inférence (`/es/_inference`) : embeddings de texte et rerank avec des services compatibles OpenAI (Ollama, vLLM, LocalAI), Cohere, Jina et Hugging Face TEI ; champs `semantic_text`, requêtes `semantic` et `neural`, retriever `text_similarity_reranker` et processeur de réponse OpenSearch `rerank`
5. Fleet et `zedagent` (`agent/`) : enrôlement des agents, gestion des politiques, envoi des fichiers de logs et des métriques système, ou agent autonome ; vue Fleet dans l'interface web
6. [Moteur de cluster](../pkg/nodes/README.md) à shards de style OpenSearch (allocation des shards, coordination par quorum, récupération entre pairs, snapshots, tiering hot/warm) et un [nœud distribué](../cmd/distribution/README.md) expérimental Raft + gRPC
7. [Robot d'exploration web](../pkg/crawler/README.md) respectant robots.txt (`cmd/zedcrawl`), avec PageRank et indexation bulk dans ZedSearch

# Benchmarks

Sept requêtes d'application de recherche sur 50 000 documents (top 10 et total exact), sur un MacBook Pro M1, chaque serveur dans Docker : une requête term filtrée, un OR sur deux champs, un tri par prix sur tous les documents, la page 101, une agrégation terms et une histogram, et le surlignage. Plus bas est meilleur, sauf pour les QPS ; « — » signifie que le moteur ne peut pas exprimer la requête (Meilisearch n'a pas d'histogramme numérique, OpenObserve ne surligne pas).

| Requête                    |     ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
| -------------------------- | ------------: | ----------------: | ----------------: | -------------: | ---------------: | --------------: |
| `filtered` (1 client)      |   **0.89 ms** |           3.43 ms |           1.38 ms |        1.44 ms |          1.87 ms |         9.44 ms |
| `filtered` (8 clients)     | **3,216 QPS** |           471 QPS |         2,742 QPS |      3,066 QPS |        1,924 QPS |         321 QPS |
| `sort_price` (50k docs)    |   **0.74 ms** |           76.0 ms |           2.52 ms |        1.58 ms |          1.09 ms |         4.40 ms |
| `multi_field` title + body |   **0.63 ms** |           2.04 ms |           1.44 ms |        1.51 ms |          1.19 ms |         21.0 ms |
| `deep_page` from 1000      |   **1.24 ms** |           3.40 ms |           1.42 ms |        1.47 ms |          12.7 ms |         18.1 ms |
| `agg_terms` `category`     |   **1.48 ms** |           5.55 ms |           1.66 ms |        2.12 ms |          1.63 ms |         18.6 ms |
| `agg_histogram` `price`    |   **1.00 ms** |           20.1 ms |           1.67 ms |        1.72 ms |                — |         18.7 ms |
| `highlight` `body`         |   **0.81 ms** |           2.27 ms |           1.96 ms |        1.97 ms |          1.92 ms |               — |

### Requêtes simples (v2)

Les requêtes v2 des mêmes sessions, un seul type de requête chacune. Meilisearch ne peut pas exprimer de OR ni de phrase exacte.

| Requête                   |     ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
| ------------------------- | ------------: | ----------------: | ----------------: | -------------: | ---------------: | --------------: |
| `term` rare               |   **0.50 ms** |           1.03 ms |           1.43 ms |        1.16 ms |          1.14 ms |         15.7 ms |
| `term` mid                |   **0.58 ms** |           1.82 ms |           1.28 ms |        1.27 ms |          1.16 ms |         17.6 ms |
| `term` keyword            |   **0.57 ms** |           0.82 ms |           1.75 ms |        2.32 ms |          1.03 ms |         5.59 ms |
| `bool` should             |   **0.63 ms** |           1.12 ms |           1.61 ms |        1.75 ms |                — |         26.7 ms |
| `bool` must               |   **1.01 ms** |           1.79 ms |           2.36 ms |        3.30 ms |          1.28 ms |         24.1 ms |
| `range`                   |   **0.64 ms** |           2.56 ms |           1.32 ms |        1.28 ms |          1.32 ms |         5.31 ms |
| `match_all`               |   **0.66 ms** |           4.12 ms |           1.48 ms |        1.38 ms |          1.03 ms |         4.68 ms |
| `prefix` `w1*`            |   **0.71 ms** |          214.5 ms |           1.87 ms |        2.66 ms |          1.32 ms |         11.9 ms |
| `match_phrase`            |   **10.3 ms** |           55.6 ms |           12.7 ms |        12.6 ms |                — |         16.3 ms |
| `bool` should (8 clients) | **5,179 QPS** |         1,292 QPS |         2,707 QPS |      2,616 QPS |                — |         131 QPS |

### Empreinte

Image Docker (non compressée), index texte des mêmes 50 000 documents (environ 25 Mo pour ZedSearch, Elasticsearch et OpenSearch après `_flush`), et mémoire / CPU sur toute la session (100 % = 1 vCPU), les données sont la médiane de trois sessions.

| Ressource             |   ZedSearch | ZincSearch 0.4.10 | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 |      OpenObserve 1.1 |
| --------------------- | ----------: | ----------------: | ----------------: | -------------: | ---------------: | -------------------: |
| Image Docker          | **56.4 MB** |           72.4 MB |          1,195 MB |       1,311 MB |           370 MB |               434 MB |
| Index texte, 50k docs |     25.9 MB |          133.3 MB |           25.8 MB |        25.7 MB |           454 MB | 14.2 MB (non indexé) |
| Mémoire au repos      |  **14 MiB** |            64 MiB |         2,674 MiB |      2,699 MiB |          179 MiB |              387 MiB |
| Mémoire max           | **388 MiB** |           858 MiB |         3,185 MiB |      3,191 MiB |        1,014 MiB |              919 MiB |
| CPU moyen             |    **134%** |              366% |              157% |           156% |             230% |                 140% |
| CPU max               |        553% |              710% |              620% |           529% |             545% |             **519%** |

### Recherche vectorielle

20 000 vecteurs cosinus de 128 dimensions, k=10, HNSW (M=16, ef_construction 100, 100 candidats), via HTTP sur la même machine et le même corpus. Plus haut est meilleur pour le recall, les QPS et l'envoi ; l'index vectoriel est la croissance des données après l'envoi, avant `_flush`.

| Mesure                       |        ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | Qdrant 1.20 | Milvus 3.0 | Weaviate 1.40 |
| ---------------------------- | ---------------: | ----------------: | -------------: | ---------------: | ----------: | ---------: | ------------: |
| Recall@10                    |            0.939 |         **0.956** |          0.823 |            0.485 |       0.891 |      0.685 |         0.595 |
| Latence, 1 client            |      **1.47 ms** |           2.56 ms |        2.09 ms |          4.71 ms |     1.84 ms |    2.70 ms |       2.28 ms |
| QPS, 8 clients               |    **1,923 QPS** |           983 QPS |      1,385 QPS |        1,025 QPS |   1,653 QPS |  1,827 QPS |     1,706 QPS |
| Envoi + indexation           | **10,070 vec/s** |       3,004 vec/s |    1,770 vec/s |        377 vec/s | 4,609 vec/s |  999 vec/s |   3,103 vec/s |
| Index vectoriel, 20k × 128 d |          28.3 MB |           63.9 MB |        75.6 MB |      **25.8 MB** |     41.4 MB |    34.3 MB |       75.6 MB |

### Recherche vectorielle, cluster de 3 nœuds

Médianes de trois exécutions : trois nœuds avec 1 réplique sur une seule VM Docker de 8 vCPU, ZedSearch avec etcd et la réplication `document`. Les nœuds partagent les CPU : cela mesure le coût de la réplication, pas la montée en charge horizontale.

| Mesure                                         |     ZedSearch ×3 |     OpenSearch ×3 |
| ---------------------------------------------- | ---------------: | ----------------: |
| Recall@10                                      |        **0.960** |             0.905 |
| Latence, 1 client                              |      **1.81 ms** |           3.59 ms |
| QPS, 8 clients                                 |    **1,611 QPS** |           970 QPS |
| Envoi + indexation                             |  **4,157 vec/s** |       1,451 vec/s |
| Index vectoriel après `_flush`, tous les nœuds |      **56.5 MB** |           70.4 MB |
| Mémoire au repos / max, tous les nœuds         | **78 / 902 MiB** | 4,517 / 5,360 MiB |

# Documentation

La documentation est disponible sur [docs/web](../docs/web/index.md), la documentation de l'API sur [zedsearch-docs](https://github.com/zedsearch/zedsearch-docs)

# Captures d'écran

## Écran de recherche

![Search screen](../docs/hots/search-26.png)

## Écran des index

![Index screen](../docs/hots/index-26.png)

# Prise en main

## Démarrage rapide

Consultez le [démarrage rapide](../docs/web/quickstart.md)

# Versions

ZedSearch compte des centaines d'installations en production.

Les binaires pour toutes les plateformes sont disponibles sur la [page des releases](https://github.com/vcaesar/zedsearch/releases/latest). Les archives par défaut ne sont pas compressées avec UPX ; pour un téléchargement plus léger, toutes les versions Linux et la version Windows amd64 sont aussi fournies dans une archive `*_upx.tar.gz` compressée avec [UPX](https://upx.github.io/) (démarrage légèrement plus lent, et certains antivirus peuvent signaler les fichiers `.exe` compressés).

## Compilation

Définissez `ZED_FIRST_ADMIN_USER` / `ZED_FIRST_ADMIN_PASSWORD` ou configurez `conf/zed.toml` (voir ci-dessous), puis exécutez depuis la racine du dépôt :

```sh
go run cmd/zedsearch/main.go      # API-only, :4080
go run cmd/zedsearch/main.go web  # with the web UI (Node.js 24+ and npm)
```

`web` produit `test/zedsearch-web`, un binaire autonome intégrant l'interface.

### Configuration

Les paramètres sont appliqués dans cet ordre : valeurs par défaut intégrées ([`pkg/config/default.toml`](../pkg/config/default.toml)), puis le fichier optionnel `conf/zed.toml` du répertoire de travail, puis les variables d'environnement non vides. Chaque clé est aussi une variable d'environnement en majuscules, par ex. `zed_server_port` → `ZED_SERVER_PORT`.

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

- N'indiquez que les clés que vous modifiez ; les clés groupées conservent leur en-tête de table de `default.toml` (`[Shard]`, `[S3]`, `[Etcd]`, ...).
- Une clé inconnue fait échouer le démarrage. Les durées sont en nanosecondes, les tailles en octets.
- Les identifiants du premier administrateur ne servent qu'à créer l'utilisateur admin au premier démarrage ; changez le mot de passe après connexion et ne committez jamais de vrais identifiants.

# Communauté

- Comment développer et contribuer à ZedSearch

  Consultez le [guide de contribution](../CONTRIBUTING.md). Consultez également la [feuille de route](../docs/web/roadmap.md)

# Exemples

Vous pouvez utiliser ZedSearch pour indexer et rechercher n'importe quelles données. [examples/](../examples) contient des scripts Python qui indexent un document, chargent des fichiers Word et les recherchent.

Projets de la communauté qui indexent et recherchent le jeu de données d'e-mails Enron avec ZincSearch (ZedSearch conserve ses endpoints `/api`) :

1. https://github.com/jorgeloaiza48/Enron-Email-DataSet
1. https://github.com/jhojanperlaza/email_search_engine
1. https://github.com/carlosarraes/zinmail
1. https://github.com/devjopa/golab-search
1. https://github.com/avaco2312/zincsearch
1. https://github.com/paolorossig/email-indexer
1. https://github.com/ulimonte05/zincsearching
