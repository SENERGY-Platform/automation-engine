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

package migrate

import (
	"context"
	"fmt"
	"net/http"

	permclient "github.com/SENERGY-Platform/permissions-v2/pkg/client"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

// PermissionsV2 reads and writes rights with an admin token, for the old topics and the topics
// of the namespaces alike.
type PermissionsV2 struct {
	Client permclient.Client
	Token  func() (string, error)
}

func (this PermissionsV2) Rights(ctx context.Context, topic string, id string) (permmodel.ResourcePermissions, bool, error) {
	token, err := this.Token()
	if err != nil {
		return permmodel.ResourcePermissions{}, false, err
	}
	resource, err, code := this.Client.GetResourceContext(ctx, token, topic, id)
	switch {
	case code == http.StatusNotFound:
		return permmodel.ResourcePermissions{}, false, nil
	case err != nil:
		return permmodel.ResourcePermissions{}, false, fmt.Errorf("rights of %v %v: %w", topic, id, err)
	}
	return resource.ResourcePermissions, true, nil
}

func (this PermissionsV2) SetRights(ctx context.Context, topic string, id string, permissions permmodel.ResourcePermissions) error {
	token, err := this.Token()
	if err != nil {
		return err
	}
	if _, err, _ = this.Client.SetPermissionContext(ctx, token, topic, id, permissions); err != nil {
		return fmt.Errorf("rights of %v %v: %w", topic, id, err)
	}
	return nil
}
