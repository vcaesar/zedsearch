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
package main

import (
	"context"
	"errors"
	"flag"
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
	addr := flag.String("addr", "127.0.0.1:8090", "listen address for the http transport")
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
	server := mcp.NewServer(client, mcp.Options{AllowWrite: *write})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, server, *transport, *addr); err != nil {
		log.Error().Err(err).Msg("zedmcp stopped")
		stop()
		os.Exit(1)
	}
}

func run(ctx context.Context, server *sdk.Server, transport, addr string) error {
	switch transport {
	case "stdio":
		return server.Run(ctx, &sdk.StdioTransport{})
	case "http":
		srv := &http.Server{
			Addr:              addr,
			Handler:           handler(server),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdown); err != nil {
				log.Error().Err(err).Msg("shutdown")
			}
		}()
		log.Info().Str("addr", addr).Msg("zedmcp listening")
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	default:
		return errors.New("unknown transport " + transport)
	}
}

// handler serves streamable HTTP. Browser cross-origin requests are rejected
// so a web page cannot drive the tools with the configured credentials, and
// idle sessions are closed so abandoned clients do not leak.
func handler(server *sdk.Server) http.Handler {
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{
		SessionTimeout: 30 * time.Minute,
	})
	return http.NewCrossOriginProtection().Handler(h)
}
