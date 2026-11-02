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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// Token is a user token including the "Bearer " prefix.
type Token string

// GetJSON reads a resource of another platform service with the token; an answer other than
// 2xx is an error with status and body.
func (this Token) GetJSON(resourceUrl string, result any) error {
	return getJson(defaultClient, this, resourceUrl, "", result)
}

// Repository reads devices, device types, protocols and device groups with the token of a user,
// as the external-task-worker did (TASK-01).
type Repository interface {
	GetDevice(ctx context.Context, token Token, id string) (models.Device, error)
	GetService(ctx context.Context, token Token, device models.Device, serviceId string) (models.Service, error)
	GetProtocol(ctx context.Context, token Token, id string) (models.Protocol, error)
	GetToken(user string) (Token, error)

	GetDeviceType(ctx context.Context, token Token, id string) (models.DeviceType, error)
	GetDeviceGroup(ctx context.Context, token Token, id string) (models.DeviceGroup, error)
}

// CachedRepository reads through the device-repository client, which passes on the trace of ctx,
// and caches per user and request; the cache invalidation signals of service-commons clear what a
// change of a resource may have made stale.
type CachedRepository struct {
	client devicerepository.Interface
	tokens auth.UserTokenSource
}

func NewRepository(repositoryUrl string, tokens auth.UserTokenSource, cacheTimeout time.Duration) (*CachedRepository, error) {
	return &CachedRepository{client: newCachedClient(repositoryUrl, cacheTimeout), tokens: tokens}, nil
}

// GetToken is the token of the user, e.g. by token exchange.
func (this *CachedRepository) GetToken(user string) (Token, error) {
	if this.tokens == nil {
		return "", fmt.Errorf("no token source for user %v", user)
	}
	token, err := this.tokens(user)
	return Token(token), err
}

// newCachedClient is a device-repository client with a cache of ttl, cleared by the cache
// invalidation signals of service-commons, which the invalidators of the server publish
func newCachedClient(repositoryUrl string, ttl time.Duration) devicerepository.Interface {
	c := devicerepository.NewClient(repositoryUrl, nil)
	devicerepository.InvalidateOnSignals(c, nil)
	return devicerepository.WithCache(c, ttl)
}

// read turns an error of the client into one that names the path and keeps the status, and
// refuses an empty answer, which the client would otherwise pass on as a zero value
func read[T any](ctx context.Context, path string, valid func(T) bool, call func(ctx context.Context) (T, error, int)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	result, err, code := call(ctx)
	if err != nil {
		return result, &statusError{status: code, message: fmt.Sprintf("GET %v: %v", path, err)}
	}
	if !valid(result) {
		return result, fmt.Errorf("GET %v: empty answer", path)
	}
	return result, nil
}

// requestTimeout bounds a request to device-repository, as defaultClient does for the others
const requestTimeout = 10 * time.Second

var defaultClient = &http.Client{Timeout: 10 * time.Second}

// getJson reads a resource with the token of a user; an answer other than 2xx is an error with
// status and body
func getJson(client *http.Client, token Token, base string, path string, result any) error {
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", string(token))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &statusError{status: resp.StatusCode, message: fmt.Sprintf("GET %v: %v %v", req.URL.Path, resp.StatusCode, strings.TrimSpace(string(body)))}
	}
	return json.NewDecoder(resp.Body).Decode(result)
}

// statusError is an answer of device-repository other than 2xx
type statusError struct {
	status  int
	message string
}

func (this *statusError) Error() string {
	return this.message
}

func (this *CachedRepository) GetDevice(ctx context.Context, token Token, id string) (models.Device, error) {
	return read(ctx, "/devices/"+id, func(d models.Device) bool { return d.Id != "" }, func(ctx context.Context) (models.Device, error, int) {
		return this.client.ReadDevice(ctx, id, string(token), devicerepomodel.READ)
	})
}

func (this *CachedRepository) GetProtocol(ctx context.Context, token Token, id string) (models.Protocol, error) {
	return read(ctx, "/protocols/"+id, func(p models.Protocol) bool { return p.Id != "" }, func(ctx context.Context) (models.Protocol, error, int) {
		return this.client.ReadProtocol(ctx, id, string(token))
	})
}

func (this *CachedRepository) GetDeviceType(ctx context.Context, token Token, id string) (models.DeviceType, error) {
	return read(ctx, "/device-types/"+id, func(dt models.DeviceType) bool { return dt.Id != "" }, func(ctx context.Context) (models.DeviceType, error, int) {
		return this.client.ReadDeviceType(ctx, id, string(token))
	})
}

func (this *CachedRepository) GetDeviceGroup(ctx context.Context, token Token, id string) (models.DeviceGroup, error) {
	return read(ctx, "/device-groups/"+id, func(g models.DeviceGroup) bool { return g.Id != "" }, func(ctx context.Context) (models.DeviceGroup, error, int) {
		return this.client.ReadDeviceGroup(ctx, id, string(token), false)
	})
}

// GetService reads the service from the device type of the device, which is cached.
func (this *CachedRepository) GetService(ctx context.Context, token Token, device models.Device, id string) (models.Service, error) {
	deviceType, err := this.GetDeviceType(ctx, token, device.DeviceTypeId)
	if err != nil {
		return models.Service{}, err
	}
	for _, s := range deviceType.Services {
		if s.Id == id {
			return s, nil
		}
	}
	return models.Service{}, fmt.Errorf("service %v not found in device type %v", id, device.DeviceTypeId)
}
