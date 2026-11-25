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
 */

package main

import (
	"crypto/tls"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

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
