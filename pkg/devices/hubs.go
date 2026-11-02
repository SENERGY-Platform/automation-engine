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

package devices

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// Hubs reads the owners of hubs from device-repository; deployments and instances of a gateway
// belong to the owner of its hub (FOG-11). Not cached: a hub may change its owner.
type Hubs struct {
	client devicerepository.Interface
}

func NewHubs(repositoryUrl string) *Hubs {
	return &Hubs{client: devicerepository.NewClient(repositoryUrl, nil)}
}

// HubOwner returns owner_id of the hub, read with the token; an unknown hub is not found.
func (this *Hubs) HubOwner(ctx context.Context, token string, id string) (string, error) {
	hub, err := read(ctx, "/hubs/"+id, func(models.Hub) bool { return true }, func(ctx context.Context) (models.Hub, error, int) {
		return this.client.ReadHub(ctx, id, token, devicerepomodel.READ)
	})
	if err != nil {
		var status *statusError
		if errors.As(err, &status) && status.status == http.StatusNotFound {
			return "", model.NotFound("%v", err)
		}
		return "", err
	}
	if hub.OwnerId == "" {
		return "", fmt.Errorf("hub %v has no owner", id)
	}
	return hub.OwnerId, nil
}
