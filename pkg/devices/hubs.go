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
	"fmt"
	"net/url"

	"github.com/SENERGY-Platform/models/go/models"
)

// Hubs reads the owners of hubs from device-repository; deployments and instances of a gateway
// belong to the owner of its hub (FOG-11).
type Hubs struct {
	url string
}

func NewHubs(repositoryUrl string) *Hubs {
	return &Hubs{url: repositoryUrl}
}

// HubOwner returns owner_id of the hub, read with the token.
func (this *Hubs) HubOwner(_ context.Context, token string, id string) (string, error) {
	var hub models.Hub
	if err := getJson(defaultClient, Token(token), this.url, "/hubs/"+url.PathEscape(id), &hub); err != nil {
		return "", err
	}
	if hub.OwnerId == "" {
		return "", fmt.Errorf("hub %v has no owner", id)
	}
	return hub.OwnerId, nil
}
