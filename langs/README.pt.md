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

[English](../README.md) | [简体中文](README.zh.md) | [繁體中文](README.zht.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Français](README.fr.md) | [Deutsch](README.de.md) | [Español](README.es.md) | [Русский](README.ru.md) | Português

# ZedSearch

ZedSearch é um mecanismo de busca que faz indexação de texto completo. É uma alternativa leve ao Elasticsearch e roda usando uma fração dos recursos. Ele usa o [bluge](https://github.com/blugelabs/bluge) (através do fork [vcaesar/riot](https://github.com/vcaesar/riot)) como biblioteca de indexação subjacente.

É muito simples e fácil de operar, ao contrário do Elasticsearch, que exige entender e ajustar algumas dezenas de parâmetros. Você pode colocar o ZedSearch em funcionamento em 2 minutos.

É um substituto direto do Elasticsearch se você apenas ingere dados usando APIs e pesquisa usando o Kibana (o Kibana não é suportado pelo ZedSearch. O ZedSearch oferece sua própria interface).

Assista ao vídeo abaixo para uma demonstração rápida do ZedSearch.

<!-- [![Zed Youtube](../screenshots/zed-youtube.jpg)](https://www.youtube.com/watch?v=aZXtuVjt1ow) -->

# Por que ZedSearch

Embora o Elasticsearch seja um produto muito bom, ele é complexo, exige muitos recursos e tem mais de uma década. Eu criei o ZedSearch para que as pessoas possam usar indexação de busca de texto completo com mais facilidade, sem muito trabalho.

# Recursos:

go + gin + react

1. Oferece capacidade de indexação de texto completo
2. Binário único para instalação e execução. Binários disponíveis nas releases para várias plataformas.
3. Interface web para consultar dados escrita em React (embutida no binário)
4. Compatibilidade com as APIs do Elasticsearch para ingestão de dados (API de registro único e bulk)
5. Autenticação pronta para uso
6. Sem esquema - não é necessário definir um esquema antecipadamente e documentos diferentes no mesmo índice podem ter campos diferentes.
7. Armazenamento de índices em disco
8. Suporte a agregações
9. [Índices de logs](../pkg/logs/README.md) opcionais com normalização do horário do evento e padrões para busca em logs

# Benchmarks

Sete consultas de aplicação de busca sobre 50.000 documentos (top 10 mais o total exato), em um MacBook Pro M1, com cada servidor no Docker: uma consulta term filtrada, um OR sobre dois campos, uma ordenação por preço sobre todos os documentos, a página 101, uma agregação terms e uma histogram, e destaque. Menor é melhor, exceto em QPS; "—" significa que o mecanismo não consegue expressar a consulta (o Meilisearch não tem histograma numérico e o OpenObserve não destaca).

| Consulta | ZedSearch | Elasticsearch 9.5 | OpenSearch 3.9 | Meilisearch 1.56 | OpenObserve 1.1 |
|---|---:|---:|---:|---:|---:|
| `filtered` (1 cliente) | **0.89 ms** | 1.38 ms | 1.44 ms | 1.87 ms | 9.44 ms |
| `filtered` (8 clientes) | **3,216 QPS** | 2,742 QPS | 3,066 QPS | 1,924 QPS | 321 QPS |
| `sort_price` (50 mil docs) | **0.74 ms** | 2.52 ms | 1.58 ms | 1.09 ms | 4.40 ms |
| `multi_field` title + body | **0.63 ms** | 1.44 ms | 1.51 ms | 1.19 ms | 21.0 ms |
| `deep_page` from 1000 | **1.24 ms** | 1.42 ms | 1.47 ms | 12.7 ms | 18.1 ms |
| `agg_terms` `category` | **1.48 ms** | 1.66 ms | 2.12 ms | 1.63 ms | 18.6 ms |
| `agg_histogram` `price` | **1.00 ms** | 1.67 ms | 1.72 ms | — | 18.7 ms |
| `highlight` `body` | **0.81 ms** | 1.96 ms | 1.97 ms | 1.92 ms | — |

### Consultas simples (v2)

As consultas v2 das mesmas sessões, um único tipo de consulta cada. O Meilisearch não consegue expressar OR nem frases exatas.

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

# Documentação

A documentação está disponível em [docs/web](../docs/web/index.md) e a documentação da API em [zedsearch-docs](https://github.com/zedsearch/zedsearch-docs)

# Capturas de tela

## Tela de busca

![Search screen](../docs/hots/search-26.png)

## Tela de índices

![Index screen](../docs/hots/index-26.png)

# Primeiros passos

## Início rápido

Veja o [início rápido](../docs/web/quickstart.md)

# Versões

O ZedSearch tem centenas de instalações em produção.

Os binários para todas as plataformas estão na [página de releases](https://github.com/vcaesar/zedsearch/releases/latest). Os arquivos padrão não são compactados com UPX; para um download menor, todas as builds para Linux e a build para Windows amd64 também são publicadas como `*_upx.tar.gz` compactado com [UPX](https://upx.github.io/) (inicialização um pouco mais lenta, e alguns antivírus podem sinalizar arquivos `.exe` compactados).

## Build

Defina `ZED_FIRST_ADMIN_USER` / `ZED_FIRST_ADMIN_PASSWORD` ou configure `conf/zed.toml` (veja abaixo) e execute a partir da raiz do repositório:

```sh
go run cmd/zedsearch/main.go      # API-only, :4080
go run cmd/zedsearch/main.go web  # with the web UI (Node.js 24+ and npm)
```

`web` gera `test/zedsearch-web`, um binário independente com a interface embutida.

### Configuração

As configurações são aplicadas nesta ordem: padrões embutidos ([`pkg/config/default.toml`](../pkg/config/default.toml)), depois o `conf/zed.toml` opcional no diretório de trabalho e, por fim, variáveis de ambiente não vazias. Cada chave em maiúsculas também é uma variável de ambiente, por exemplo `zed_server_port` → `ZED_SERVER_PORT`.

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

- Inclua apenas as chaves que você alterar; chaves agrupadas mantêm o cabeçalho de tabela do `default.toml` (`[Shard]`, `[S3]`, `[Etcd]`, ...).
- Chaves desconhecidas fazem a inicialização falhar. Durações são em nanossegundos e tamanhos em bytes.
- As credenciais do primeiro administrador só criam o usuário admin na primeira inicialização; altere a senha após o login e nunca faça commit de credenciais reais.

# Comunidade

- Como desenvolver e contribuir com o ZedSearch

  Veja o [guia de contribuição](../CONTRIBUTING.md). Veja também o [roadmap](../docs/web/roadmap.md)

# Exemplos

Você pode usar o ZedSearch para indexar e pesquisar quaisquer dados. Aqui estão alguns exemplos que a comunidade criou para indexar e pesquisar o conjunto de dados de e-mails da Enron usando o zedsearch:

1. https://github.com/jorgeloaiza48/Enron-Email-DataSet
1. https://github.com/jhojanperlaza/email_search_engine
1. https://github.com/carlosarraes/zinmail
1. https://github.com/devjopa/golab-search
1. https://github.com/avaco2312/zincsearch
1. https://github.com/paolorossig/email-indexer
1. https://github.com/ulimonte05/zincsearching
