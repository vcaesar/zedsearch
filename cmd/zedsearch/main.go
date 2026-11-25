/* Copyright 2022 Zinc Labs Inc. and Contributors
* Copyright 2026 AtomAI.
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
please refer to [AGPL-3.0](./LICENSE-EE)) and LICENSE-EE for details,
and production use of the Licensed Work requires a commercial license agreement with Zedsearch.
Contact info@atomai.cc for licensing.
*/

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/grafana/pyroscope-go"
	"github.com/rs/zerolog/log"

	"github.com/vcaesar/zedsearch"
	"github.com/vcaesar/zedsearch/pkg/cluster"
	"github.com/vcaesar/zedsearch/pkg/cluster/replication"
	"github.com/vcaesar/zedsearch/pkg/config"
	"github.com/vcaesar/zedsearch/pkg/core"
	"github.com/vcaesar/zedsearch/pkg/meta"
	"github.com/vcaesar/zedsearch/pkg/metadata"
	"github.com/vcaesar/zedsearch/pkg/routes"
	"github.com/vcaesar/zedsearch/pkg/tracing"
	"github.com/vcaesar/zedsearch/pkg/webbuild"
	"github.com/vcaesar/zedsearch/pkg/zhttp"
)

// @title           ZedSearch engine API
// @version         1.0.0
// @description     ZedSearch engine API documents https://github.com/vcaesar/zedsearch
// @termsOfService  http://swagger.io/terms/

// @license.name    Apache 2.0
// @license.url     http://www.apache.org/licenses/LICENSE-2.0.html

// @contact.name    ZedSearch
// @contact.url     https://github.com/vcaesar/zedsearch

// @securityDefinitions.basic BasicAuth
// @BasePath        /
// @schemes http https
func main() {
	if len(os.Args) > 1 && os.Args[1] == "web" {
		os.Args = append(os.Args[:1], os.Args[2:]...)
		front, err := zedsearch.GetFrontendAssets()
		if err != nil {
			log.Fatal().Err(err).Msg("frontend assets")
		}
		if front == nil {
			binary, err := webbuild.Build()
			if err != nil {
				log.Fatal().Err(err).Msg("build web binary")
			}
			// Package initializers opened storage; release it before restarting.
			if err := errors.Join(core.ZED_INDEX_LIST.Close(), metadata.Close()); err != nil {
				log.Fatal().Err(err).Msg("close storage before web startup")
			}
			if err := webbuild.Run(binary, os.Args[1:]); err != nil {
				log.Fatal().Err(err).Msg("run web binary")
			}
			return
		}
	}

	// Version
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("zedsearch version %s\n", meta.Version)
		os.Exit(0)
	}
	log.Info().Msgf("Starting ZedSearch %s", meta.Version)

	// Initialize telemetry
	telemetry()
	// Initialize sentry
	sentries()
	// Continuous profiling
	profiling()
	// Distributed tracing
	stopTracing, err := tracing.Start(context.Background(), tracing.Config{
		Enable:   config.Global.TracingEnable,
		Endpoint: config.Global.TracingEndpoint,
		Service:  "zedsearch",
		Version:  meta.Version,
		Node:     config.Global.Cluster.NodeID,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Tracing start")
	}

	// HTTP init
	app := zhttp.New()
	routes.Setup(app)

	// Join the cluster before serving, so the first request already routes
	// to the node that owns the index. No-op unless clustering is enabled.
	if err := cluster.Start(context.Background()); err != nil {
		log.Fatal().Err(err).Msg("Cluster start")
	}
	// Keep every index on more than one node: document or segment
	// replication for disk indexes, the remote translog for object storage.
	if err := replication.Start(context.Background()); err != nil {
		log.Fatal().Err(err).Msg("Replication start")
	}

	// Move indexes between hot, warm and cold by their lifecycle.
	// No-op unless tiering is enabled.
	core.StartTiering(context.Background())

	// Run the server
	PORT := config.Global.ServerPort
	ADDRESS := config.Global.ServerAddress
	addr := ADDRESS + ":" + PORT

	done := shutdown(func(grace bool, done chan<- struct{}) {
		// close http server
		if grace {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*60)
			defer cancel()
			if err := app.Shutdown(ctx); err != nil {
				log.Fatal().Err(err).Msg("Server Shutdown")
			}
		} else if err := app.Close(); err != nil {
			log.Error().Err(err).Msg("Server Close")
		}

		log.Info().Msg("Index closing...")
		// stop replicating, then leave the cluster so peers stop routing here
		replication.Stop()
		cluster.Stop()
		// close indexes
		err := core.ZED_INDEX_LIST.Close()
		log.Info().Err(err).Msgf("Index closed")
		// close metadata
		err = metadata.Close()
		log.Info().Err(err).Msgf("Metadata closed")
		// deliver buffered error events; returns at once when sentry is off
		sentry.Flush(2 * time.Second)
		// export buffered spans; returns at once when tracing is off
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := stopTracing(ctx); err != nil {
			log.Error().Err(err).Msg("Tracing shutdown")
		}
		cancel()

		done <- struct{}{}
	})

	ln, err := listener(addr)
	if err != nil {
		log.Fatal().Err(err).Msg("Listen on " + addr)
	}
	log.Info().Msg("Listen on " + addr + " with " + zhttp.Backend)
	if err := app.Serve(ln); err != nil {
		log.Fatal().Err(err).Msg("Server closed unexpect")
	}
	log.Info().Msg("Server closed")

	<-done

	log.Info().Msg("Server shutdown ok")
}

// listener opens the server socket; tests replace it to inject socket failures.
var listener = listen

// listen opens the server socket, TLS when a certificate is configured.
func listen(addr string) (net.Listener, error) {
	certFile := config.Global.ServerTLSCertificateFile
	keyFile := config.Global.ServerTLSKeyFile
	if certFile == "" || keyFile == "" {
		return net.Listen("tcp", addr)
	}

	certs, err := newCertReloader(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	// set minimum TLS version (1.2) and intermediate cipher suites
	return tls.Listen("tcp", addr, &tls.Config{
		GetCertificate: certs.GetCertificate,
		MinVersion:     tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
		NextProtos: zhttp.TLSNextProtos,
	})
}

// certReloader serves the key pair of certFile and keyFile and loads it again
// when either file changes, so a renewed certificate (cert-manager rewrites
// the mounted Secret) is served without a restart.
type certReloader struct {
	certFile, keyFile string

	mu      sync.Mutex
	cert    *tls.Certificate
	certMod time.Time
	keyMod  time.Time
	failed  string
}

func newCertReloader(certFile, keyFile string) (*certReloader, error) {
	r := &certReloader{certFile: certFile, keyFile: keyFile}
	certMod, keyMod, err := r.modTimes()
	if err != nil {
		return nil, err
	}
	if err := r.load(certMod, keyMod); err != nil {
		return nil, err
	}
	return r, nil
}

// GetCertificate is the tls.Config hook. A changed pair that fails to load,
// such as one caught halfway through a rewrite, keeps the previous
// certificate and is tried again on the next handshake.
func (r *certReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	certMod, keyMod, err := r.modTimes()
	if err == nil && (!certMod.Equal(r.certMod) || !keyMod.Equal(r.keyMod)) {
		err = r.load(certMod, keyMod)
	}
	// log a failure once, not on every handshake until the files are fixed
	if err == nil {
		r.failed = ""
	} else if err.Error() != r.failed {
		r.failed = err.Error()
		log.Warn().Err(err).Msg("Reload TLS certificate, serving the previous one")
	}
	return r.cert, nil
}

func (r *certReloader) modTimes() (certMod, keyMod time.Time, err error) {
	ci, err := os.Stat(r.certFile)
	if err != nil {
		return certMod, keyMod, err
	}
	ki, err := os.Stat(r.keyFile)
	if err != nil {
		return certMod, keyMod, err
	}
	return ci.ModTime(), ki.ModTime(), nil
}

func (r *certReloader) load(certMod, keyMod time.Time) error {
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return err
	}
	r.cert, r.certMod, r.keyMod = &cert, certMod, keyMod
	return nil
}

func telemetry() {
	core.Telemetry.Instance()
	core.Telemetry.Event("server_start", nil)
	core.Telemetry.Cron()
}

func sentries() {
	if !config.Global.SentryEnable {
		return
	}
	if config.Global.SentryDSN == "" {
		return
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:     config.Global.SentryDSN,
		Release: "zedsearch@" + meta.Version,
	})
	if err != nil {
		log.Print("sentry.Init: ", err.Error())
	}
}

func profiling() {
	if !config.Global.ProfilerEnable {
		return
	}
	if config.Global.ProfilerServer == "" {
		return
	}

	ProfileID := config.Global.ProfilerFriendlyProfileID
	if ProfileID == "" {
		ProfileID = strings.ToLower(core.Telemetry.GetInstanceID())
	}

	_, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: "zedsearch-" + ProfileID,

		// replace this with the address of pyroscope server
		ServerAddress: config.Global.ProfilerServer,

		// you can disable logging by setting this to nil
		// Logger: pyroscope.StandardLogger,
		Logger: nil,

		// optionally, if authentication is enabled, specify the API key:
		// AuthToken: os.Getenv("PYROSCOPE_AUTH_TOKEN"),
		AuthToken: config.Global.ProfilerAPIKey, //nolint:staticcheck // token auth; BasicAuthUser/Password needs a new config field

		// by default all profilers are enabled,
		// but you can select the ones you want to use:
		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,
		},
	})
	if err != nil {
		log.Print("pyroscope.Start: ", err.Error())
	}
}

// shutdown support twice signal must exit
func shutdown(stop func(grace bool, done chan<- struct{})) <-chan struct{} {
	done := make(chan struct{})
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGQUIT, os.Interrupt, syscall.SIGTERM)
	go func() {
		s := <-sig
		go stop(s != syscall.SIGQUIT, done)
		<-sig
		os.Exit(128 + int(s.(syscall.Signal))) // second signal. Exit directly.
	}()
	return done
}
