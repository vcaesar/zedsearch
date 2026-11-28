/* Copyright 2026 AtomAI.
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
*
Part of this work fall under the AtomAI Enterprise Edition (EE)
and are licensed under the Business Source License 1.1,
if you are the Cloud companies or your companies with revenue greater than $1M,
please refer to [AGPL-3.0](./LICENSE-AGPL)) and [LICENSE-EE](./LICENSE-EE) for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.

The other parts of this work are licensed under the [Apache license](./LICENSE).
*/

// zedmcp is a Model Context Protocol server for ZedSearch.
//
//	zedmcp                                  # stdio, for Claude Desktop, Cursor, ...
//	zedmcp -transport http -addr 127.0.0.1:8090
//
// ZED_MCP_URL, ZED_MCP_USERNAME and ZED_MCP_PASSWORD set the ZedSearch server.
// ZED_MCP_TOKEN is the bearer token HTTP clients must send; it is required
// when -addr is not a loopback address.
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog/log"

	"github.com/vcaesar/zedsearch/agent/mcp"
)

func main() {
	transport := flag.String("transport", "stdio", "stdio or http (streamable HTTP)")
	addr := flag.String("addr", "127.0.0.1:8090",
		"listen address for the http transport; a non-loopback address exposes the tools to the network and requires ZED_MCP_TOKEN")
	certFile := flag.String("tls-cert", "", "TLS certificate file for the http transport")
	keyFile := flag.String("tls-key", "", "TLS key file for the http transport")
	write := flag.Bool("write", false, "enable the index_document and delete_document tools")
	version := flag.Bool("version", false, "print version")
	flag.Parse()
	if *version {
		log.Info().Str("version", mcp.Version).Msg("zedmcp")
		return
	}

	baseURL := os.Getenv("ZED_MCP_URL")
	if baseURL == "" {
		baseURL = "http://localhost:4080"
	}
	client := mcp.NewClient(baseURL, os.Getenv("ZED_MCP_USERNAME"), os.Getenv("ZED_MCP_PASSWORD"))
	if client.CleartextAuth() {
		log.Warn().Str("url", client.Endpoint()).Msg("ZedSearch credentials are sent unencrypted over http; use https for remote servers")
	}
	server := mcp.NewServer(client, mcp.Options{AllowWrite: *write})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := config{transport: *transport, addr: *addr, token: os.Getenv("ZED_MCP_TOKEN"), certFile: *certFile, keyFile: *keyFile}
	if err := run(ctx, server, cfg); err != nil {
		log.Error().Err(err).Msg("zedmcp stopped")
		stop()
		os.Exit(1)
	}
}

type config struct {
	transport, addr, token, certFile, keyFile string
}

func run(ctx context.Context, server *sdk.Server, cfg config) error {
	switch cfg.transport {
	case "stdio":
		return server.Run(ctx, &sdk.StdioTransport{})
	case "http":
		if (cfg.certFile == "") != (cfg.keyFile == "") {
			return errors.New("-tls-cert and -tls-key must be set together")
		}
		if !loopback(cfg.addr) {
			if cfg.token == "" {
				return errors.New("ZED_MCP_TOKEN is required to listen on non-loopback address " + cfg.addr)
			}
			if cfg.certFile == "" {
				log.Warn().Str("addr", cfg.addr).Msg("the bearer token is sent unencrypted; set -tls-cert and -tls-key or terminate TLS at a proxy")
			}
		}
		srv := &http.Server{
			Addr:              cfg.addr,
			Handler:           handler(server, cfg.token),
			ReadHeaderTimeout: 10 * time.Second,
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdown); err != nil {
				log.Error().Err(err).Msg("shutdown")
				if err := srv.Close(); err != nil {
					log.Error().Err(err).Msg("close")
				}
			}
		}()
		log.Info().Str("addr", cfg.addr).Bool("tls", cfg.certFile != "").Msg("zedmcp listening")
		var err error
		if cfg.certFile != "" {
			err = srv.ListenAndServeTLS(cfg.certFile, cfg.keyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		<-done
		return nil
	default:
		return errors.New("unknown transport " + cfg.transport)
	}
}

// loopback reports whether addr only listens on a loopback interface.
func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	return err == nil && mcp.IsLoopback(host)
}

// handler serves streamable HTTP. Browser cross-origin requests are rejected
// so a web page cannot drive the tools with the configured credentials,
// clients must send the bearer token when one is set, and idle sessions are
// closed so abandoned clients do not leak.
func handler(server *sdk.Server, token string) http.Handler {
	var h http.Handler = sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{
		SessionTimeout: 30 * time.Minute,
	})
	if token != "" {
		h = requireToken(h, token)
	}
	return http.NewCrossOriginProtection().Handler(h)
}

func requireToken(next http.Handler, token string) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			// Closing and expiring reads skip draining an unread body an
			// unauthenticated client could withhold to pin the connection.
			w.Header().Set("Connection", "close")
			if err := http.NewResponseController(w).SetReadDeadline(time.Now()); err != nil && !errors.Is(err, http.ErrNotSupported) {
				log.Debug().Err(err).Msg("expire unauthenticated read")
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="zedmcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
