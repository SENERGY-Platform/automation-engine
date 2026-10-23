/*
 * Copyright 2026 InfAI (CC SES)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package httpcall is the task type "http" for HTTP calls users define (BPMN-10): method, URL,
// headers and body as inputs, statusCode and response as outputs. Which targets are allowed is
// configured per namespace, separately for cloud and gateway (NS-04).
package httpcall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

const TaskType = "http"

const (
	Cloud = "cloud"
	Fog   = "fog"
)

type Namespaces interface {
	Get(name string) (model.Namespace, bool)
}

type Type struct {
	namespaces Namespaces
	mode       string
	// the policy a gateway sets for itself; it replaces the one of every namespace
	local *model.HttpPolicy
	// replaced in tests, e.g. to reach a test server on a loopback address
	resolver *net.Resolver
}

func New(namespaces Namespaces, mode string) *Type {
	return &Type{namespaces: namespaces, mode: mode, resolver: net.DefaultResolver}
}

// SetLocalPolicy makes policy the one of every namespace, as a gateway configures it (NS-04).
func (this *Type) SetLocalPolicy(policy model.HttpPolicy) {
	policy = policy.WithDefaults()
	this.local = &policy
}

func (this *Type) policy(namespace string) model.HttpPolicy {
	if this.local != nil {
		return *this.local
	}
	ns, _ := this.namespaces.Get(namespace)
	ns = ns.WithDefaults()
	if this.mode == Fog {
		return ns.Http.Fog
	}
	return ns.Http.Cloud
}

func hostMatches(patterns []string, host string) bool {
	host = strings.ToLower(host)
	for _, p := range patterns {
		p = strings.ToLower(p)
		if p == host || (strings.HasPrefix(p, "*.") && strings.HasSuffix(host, p[1:])) {
			return true
		}
	}
	return false
}

func inNetworks(networks []string, ip net.IP) bool {
	for _, n := range networks {
		if _, network, err := net.ParseCIDR(n); err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

var errDenied = errors.New("target not allowed")

// target checks scheme, host and port of a URL; allowedByName reports an allow list entry for
// the host name, otherwise the resolved address has to be in an allowed network
func target(policy model.HttpPolicy, u *url.URL) (allowedByName bool, err error) {
	if u.Scheme != "http" && u.Scheme != "https" {
		return false, fmt.Errorf("%w: scheme %q", errDenied, u.Scheme)
	}
	host := u.Hostname()
	if hostMatches(policy.DenyHosts, host) {
		return false, fmt.Errorf("%w: host %v", errDenied, host)
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	if len(policy.AllowPorts) > 0 {
		p, _ := strconv.Atoi(port)
		if !slices.Contains(policy.AllowPorts, p) {
			return false, fmt.Errorf("%w: port %v", errDenied, port)
		}
	}
	allowedByName = hostMatches(policy.AllowHosts, host) || (len(policy.AllowHosts) == 0 && len(policy.AllowNetworks) == 0)
	return allowedByName, nil
}

// client checks every connection on the address it actually dials, also after redirects
func (this *Type) client(policy model.HttpPolicy, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: timeout, Resolver: this.resolver}
	transport := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout}
	transport.DialContext = func(ctx context.Context, network string, address string) (net.Conn, error) {
		allowedByName, _ := ctx.Value(allowedKey{}).(bool)
		d := *dialer
		d.Control = func(_ string, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || inNetworks(policy.DenyNetworks, ip) {
				return fmt.Errorf("%w: address %v", errDenied, host)
			}
			if !allowedByName && !inNetworks(policy.AllowNetworks, ip) {
				return fmt.Errorf("%w: address %v", errDenied, host)
			}
			return nil
		}
		return d.DialContext(ctx, network, address)
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			allowedByName, err := target(policy, req.URL)
			if err != nil {
				return err
			}
			*req = *req.WithContext(context.WithValue(req.Context(), allowedKey{}, allowedByName))
			return nil
		},
	}
}

type allowedKey struct{}

func text(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	default:
		raw, err := json.Marshal(v)
		return string(raw), err
	}
}

// Execute calls the URL; any answer completes the task with statusCode and response, the
// process decides what a status means. A denied target fails without retry.
func (this *Type) Execute(ctx context.Context, task engine.Task) (engine.TaskResult, error) {
	policy := this.policy(task.Namespace)
	if !policy.Enabled {
		return engine.TaskResult{}, &engine.PermanentError{Message: "http calls are disabled in namespace " + task.Namespace}
	}
	rawUrl, _ := task.Input["url"].(string)
	u, err := url.Parse(rawUrl)
	if err != nil || rawUrl == "" {
		return engine.TaskResult{}, &engine.PermanentError{Message: fmt.Sprintf("invalid url %q", rawUrl)}
	}
	allowedByName, err := target(policy, u)
	if err != nil {
		return engine.TaskResult{}, &engine.PermanentError{Message: err.Error()}
	}
	method := strings.ToUpper(fmt.Sprint(task.Input["method"]))
	if task.Input["method"] == nil {
		method = http.MethodGet
	}
	body, err := text(task.Input["body"])
	if err != nil {
		return engine.TaskResult{}, &engine.PermanentError{Message: "body: " + err.Error()}
	}
	timeout, _ := time.ParseDuration(policy.Timeout)
	requestCtx := context.WithValue(ctx, allowedKey{}, allowedByName)
	req, err := http.NewRequestWithContext(requestCtx, method, u.String(), bytes.NewReader([]byte(body)))
	if err != nil {
		return engine.TaskResult{}, &engine.PermanentError{Message: err.Error()}
	}
	headers := map[string]any{}
	switch h := task.Input["headers"].(type) {
	case map[string]any:
		headers = h
	case string:
		if h != "" && json.Unmarshal([]byte(h), &headers) != nil {
			return engine.TaskResult{}, &engine.PermanentError{Message: "headers must be a map"}
		}
	}
	for name, value := range headers {
		req.Header.Set(name, fmt.Sprint(value))
	}
	resp, err := this.client(policy, timeout).Do(req)
	if err != nil {
		if errors.Is(err, errDenied) {
			return engine.TaskResult{}, &engine.PermanentError{Message: err.Error()}
		}
		return engine.TaskResult{}, err
	}
	defer resp.Body.Close()
	content, err := io.ReadAll(io.LimitReader(resp.Body, int64(policy.MaxResponseBytes)+1))
	if err != nil {
		return engine.TaskResult{}, err
	}
	if len(content) > policy.MaxResponseBytes {
		return engine.TaskResult{}, &engine.PermanentError{Message: fmt.Sprintf("response exceeds %v bytes", policy.MaxResponseBytes)}
	}
	return engine.TaskResult{Output: map[string]any{"statusCode": float64(resp.StatusCode), "response": string(content)}}, nil
}

// Do sends a request under the HTTP policy of a namespace (NS-04), e.g. a generic watch request
// of the watcher (TASK-27). The caller closes the body of the response.
func (this *Type) Do(namespace string, req *http.Request) (*http.Response, error) {
	policy := this.policy(namespace)
	if !policy.Enabled {
		return nil, fmt.Errorf("%w: http calls are disabled in namespace %v", errDenied, namespace)
	}
	allowedByName, err := target(policy, req.URL)
	if err != nil {
		return nil, err
	}
	timeout, _ := time.ParseDuration(policy.Timeout)
	return this.client(policy, timeout).Do(req.WithContext(context.WithValue(req.Context(), allowedKey{}, allowedByName)))
}
