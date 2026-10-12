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

package prepare

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	permclient "github.com/SENERGY-Platform/permissions-v2/pkg/client"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

// Access checks that the deploying user may use the selected devices, device groups and
// imports (DEP-06), as process-deployment does in EnsureDeploymentSelectionAccess: execute on
// the permissions-v2 topics devices and device-groups, and imports visible in import-deploy.
type Access struct {
	permissions     permclient.Client
	importDeployUrl string
	http            *http.Client
}

func NewAccess(permissions permclient.Client, importDeployUrl string) *Access {
	return &Access{permissions: permissions, importDeployUrl: importDeployUrl, http: &http.Client{Timeout: 30 * time.Second}}
}

func (this *Access) Check(ctx context.Context, token string, selections map[string]model.Selection) error {
	var devices, groups, imports []string
	for _, s := range selections {
		if s.DeviceId != "" && !slices.Contains(devices, s.DeviceId) {
			devices = append(devices, s.DeviceId)
		}
		if s.DeviceGroupId != "" && !slices.Contains(groups, s.DeviceGroupId) {
			groups = append(groups, s.DeviceGroupId)
		}
		if s.ImportId != "" && !slices.Contains(imports, s.ImportId) {
			imports = append(imports, s.ImportId)
		}
	}
	if this.permissions != nil {
		for topic, ids := range map[string][]string{"devices": devices, "device-groups": groups} {
			if len(ids) == 0 {
				continue
			}
			access, err, code := this.permissions.CheckMultiplePermissionsContext(ctx, token, topic, ids, permmodel.Execute)
			if err != nil {
				return model.Unavailable("permissions-v2 (%v): %v", code, err)
			}
			if denied := deniedIds(access, ids); len(denied) > 0 {
				return model.Forbidden("missing execute right on %v %v", topic, denied)
			}
		}
	}
	if len(imports) > 0 && this.importDeployUrl != "" {
		visible, err := this.visibleImports(ctx, token)
		if err != nil {
			return err
		}
		var denied []string
		for _, id := range imports {
			if !visible[id] {
				denied = append(denied, id)
			}
		}
		if len(denied) > 0 {
			return model.Forbidden("missing access to imports %v", denied)
		}
	}
	return nil
}

func deniedIds(access map[string]bool, ids []string) (result []string) {
	for _, id := range ids {
		if !access[id] {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

func (this *Access) visibleImports(ctx context.Context, token string) (map[string]bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, this.importDeployUrl+"/instances", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)
	if err = tracing.Inject(ctx, req); err != nil {
		return nil, err
	}
	resp, err := this.http.Do(req)
	if err != nil {
		return nil, model.Unavailable("import-deploy: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, model.Unavailable("import-deploy answered %v: %s", resp.Status, body)
	}
	var instances []struct {
		Id string `json:"id"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&instances); err != nil {
		return nil, fmt.Errorf("import-deploy: %w", err)
	}
	result := map[string]bool{}
	for _, i := range instances {
		result[i.Id] = true
	}
	return result, nil
}
