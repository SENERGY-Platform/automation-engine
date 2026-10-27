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

package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/SENERGY-Platform/automation-engine/docs"
)

// AsyncApiDocId names the document at api-docs-provider, by the convention of the platform services
const AsyncApiDocId = "github_com_SENERGY-Platform_automation-engine"

// PublishAsyncApi uploads an AsyncAPI document to api-docs-provider, which shows it in API Docs of
// the web-ui. A later upload replaces it.
func PublishAsyncApi(ctx context.Context, client *http.Client, baseUrl string, doc []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	u, err := url.JoinPath(baseUrl, "storage", "asyncapi", AsyncApiDocId)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(doc))
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("api-docs-provider answered %v: %s", resp.StatusCode, body)
	}
	return nil
}

// publishAsyncApi uploads docs/asyncapi.json once on start; gateways have no provider
func (this *Server) publishAsyncApi(ctx context.Context) {
	if this.config.ApiDocsProviderBaseUrl == "" || this.config.Mode == "fog" {
		return
	}
	if err := PublishAsyncApi(ctx, http.DefaultClient, this.config.ApiDocsProviderBaseUrl, docs.AsyncApiDoc); err != nil {
		// API Docs lack the document until the next start; the engine itself is not affected
		this.logger.Warn("unable to upload the asyncapi document to api-docs-provider", "error", err)
		return
	}
	this.logger.Info("uploaded the asyncapi document to api-docs-provider", "id", AsyncApiDocId)
}
