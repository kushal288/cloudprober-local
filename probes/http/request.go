// Copyright 2019-2020 The Cloudprober Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package http

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloudprober/cloudprober/targets/endpoint"
)

const relURLLabel = "relative_url"

// requestBody encapsulates the request body and implements the io.Reader()
// interface.
type requestBody struct {
	b []byte
}

// Read implements the io.Reader interface. Instead of using buffered read,
// it simply copies the bytes to the provided slice in one go (depending on
// the input slice capacity) and returns io.EOF. Buffered reads require
// resetting the buffer before re-use, restricting our ability to use the
// request object concurrently.
func (rb *requestBody) Read(p []byte) (int, error) {
	return copy(p, rb.b), io.EOF
}

func hostWithPort(host string, port int) string {
	if port == 0 {
		return host
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// hostHeaderForTarget computes request's Host header for a target.
//  - If host header is set in the probe, it overrides everything else.
//  - If target's fqdn is provided in its labels, use that along with the port.
//  - Finally, use target's name with port.
func hostHeaderForTarget(target endpoint.Endpoint, probeHostHeader string, port int) string {
	if probeHostHeader != "" {
		return probeHostHeader
	}

	if target.Labels["fqdn"] != "" {
		return hostWithPort(target.Labels["fqdn"], port)
	}

	return hostWithPort(target.Name, port)
}

func urlHostForTarget(target endpoint.Endpoint) string {
	if target.Labels["fqdn"] != "" {
		return target.Labels["fqdn"]
	}

	return target.Name
}

func relURLForTarget(target endpoint.Endpoint, probeURL string) string {
	if probeURL != "" {
		return probeURL
	}

	if target.Labels[relURLLabel] != "" {
		return target.Labels[relURLLabel]
	}

	return ""
}

func (p *Probe) httpRequestForTarget(target endpoint.Endpoint) *http.Request {
	// Prepare HTTP.Request for Client.Do
	port := int(p.c.GetPort())
	// If port is not configured explicitly, use target's port if available.
	if port == 0 {
		port = target.Port
	}

	urlHost := urlHostForTarget(target)
	ipForLabel := ""

	resolveFirst := false
	if p.c.ResolveFirst != nil {
		resolveFirst = p.c.GetResolveFirst()
	} else {
		resolveFirst = target.IP != nil
	}
	if resolveFirst {
		ip, err := target.Resolve(p.opts.IPVersion, p.opts.Targets)
		if err != nil {
			p.l.Error("target: ", target.Name, ", resolve error: ", err.Error())
			return nil
		}

		ipStr := ip.String()
		urlHost, ipForLabel = ipStr, ipStr
	}

	for _, al := range p.opts.AdditionalLabels {
		al.UpdateForTarget(target, ipForLabel, port)
	}

	// Put square brackets around literal IPv6 hosts. This is the same logic as
	// net.JoinHostPort, but we cannot use net.JoinHostPort as it works only for
	// non default ports.
	if strings.IndexByte(urlHost, ':') >= 0 {
		urlHost = "[" + urlHost + "]"
	}

	url := fmt.Sprintf("%s://%s%s", p.protocol, hostWithPort(urlHost, port), relURLForTarget(target, p.url))

	// Prepare request body
	var body io.Reader
	if len(p.requestBody) > 0 {
		body = &requestBody{p.requestBody}
	}
	req, err := http.NewRequest(p.method, url, body)
	if err != nil {
		p.l.Error("target: ", target.Name, ", error creating HTTP request: ", err.Error())
		return nil
	}

	req.ContentLength = int64(len(p.requestBody))

	var probeHostHeader string
	for _, header := range p.c.GetHeaders() {
		if header.GetName() == "Host" {
			probeHostHeader = header.GetValue()
			continue
		}
		req.Header.Set(header.GetName(), header.GetValue())
	}

	// Host header is set by http.NewRequest based on the URL, update it based
	// on various conditions.
	req.Host = hostHeaderForTarget(target, probeHostHeader, port)

	return req
}
