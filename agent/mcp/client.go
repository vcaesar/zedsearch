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

package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vcaesar/zedsearch/pkg/zutils/json"
)

// Client calls the Elasticsearch-compatible API of a ZedSearch server.
type Client struct {
	URL      string
	Username string
	Password string
	HTTP     *http.Client
}

// NewClient returns a client with basic auth and a request timeout.
// Redirects never carry credentials over cleartext HTTP to a remote host.
func NewClient(baseURL, username, password string) *Client {
	return &Client{
		URL:      strings.TrimRight(baseURL, "/"),
		Username: username,
		Password: password,
		HTTP:     &http.Client{Timeout: 60 * time.Second, CheckRedirect: checkRedirect},
	}
}

// CleartextAuth reports whether credentials, configured or embedded in the
// URL, are sent over plain HTTP to a non-loopback host.
func (c *Client) CleartextAuth() bool {
	u, err := url.Parse(c.URL)
	if err != nil || (c.Username == "" && u.User == nil) {
		return false
	}
	return cleartext(u)
}

// Endpoint returns the scheme and host of the server URL, without
// credentials, path or query, so it is safe to log.
func (c *Client) Endpoint() string {
	u, err := url.Parse(c.URL)
	if err != nil {
		return "invalid URL"
	}
	return u.Scheme + "://" + u.Host
}

// IsLoopback reports whether host is localhost or a loopback IP.
func IsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func cleartext(u *url.URL) bool {
	return u.Scheme != "https" && !IsLoopback(u.Hostname())
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if cleartext(req.URL) {
		req.Header.Del("Authorization")
	}
	return nil
}

// Do sends body (JSON encoded when not nil) and returns the raw response.
func (c *Client) Do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Username != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))
	}
	return data, nil
}
