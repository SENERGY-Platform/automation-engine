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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/models/go/models"
	"github.com/SENERGY-Platform/service-commons/pkg/cache"
	"github.com/SENERGY-Platform/service-commons/pkg/signal"
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
	GetDevice(token Token, id string) (models.Device, error)
	GetService(token Token, device models.Device, serviceId string) (models.Service, error)
	GetProtocol(token Token, id string) (models.Protocol, error)
	GetToken(user string) (Token, error)

	GetDeviceType(token Token, id string) (models.DeviceType, error)
	GetDeviceGroup(token Token, id string) (models.DeviceGroup, error)
}

// CachedRepository caches the answers of device-repository; the cache invalidation signals of
// service-commons clear the entries of changed resources.
type CachedRepository struct {
	url     string
	tokens  auth.UserTokenSource
	cache   *cache.Cache
	timeout time.Duration
	http    *http.Client
}

func NewRepository(repositoryUrl string, tokens auth.UserTokenSource, cacheTimeout time.Duration) (*CachedRepository, error) {
	c, err := cache.New(cache.Config{
		CacheInvalidationSignalHooks: map[cache.Signal]cache.ToKey{
			signal.Known.CacheInvalidationAll:        nil,
			signal.Known.DeviceCacheInvalidation:     func(value string) string { return "device." + value },
			signal.Known.DeviceTypeCacheInvalidation: func(value string) string { return "device-type." + value },
			signal.Known.DeviceGroupInvalidation:     func(value string) string { return "device-group." + value },
			signal.Known.ProtocolInvalidation:        func(value string) string { return "protocol." + value },
		},
	})
	if err != nil {
		return nil, err
	}
	return &CachedRepository{url: repositoryUrl, tokens: tokens, cache: c, timeout: cacheTimeout, http: defaultClient}, nil
}

// GetToken is the token of the user, e.g. by token exchange.
func (this *CachedRepository) GetToken(user string) (Token, error) {
	if this.tokens == nil {
		return "", fmt.Errorf("no token source for user %v", user)
	}
	token, err := this.tokens(user)
	return Token(token), err
}

func cached[T any](this *CachedRepository, key string, get func() (T, error), valid func(T) bool) (T, error) {
	return cache.Use(this.cache, key, get, func(value T) error {
		if !valid(value) {
			return errors.New("invalid " + key + " loaded from cache")
		}
		return nil
	}, this.timeout)
}

func (this *CachedRepository) get(token Token, path string, result any) error {
	return getJson(this.http, token, this.url, path, result)
}

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

func (this *CachedRepository) GetDevice(token Token, id string) (models.Device, error) {
	return cached(this, "device."+id, func() (result models.Device, err error) {
		return result, this.get(token, "/devices/"+url.QueryEscape(id), &result)
	}, func(d models.Device) bool { return d.Id != "" })
}

func (this *CachedRepository) GetProtocol(token Token, id string) (models.Protocol, error) {
	return cached(this, "protocol."+id, func() (result models.Protocol, err error) {
		return result, this.get(token, "/protocols/"+url.QueryEscape(id), &result)
	}, func(p models.Protocol) bool { return p.Id != "" })
}

func (this *CachedRepository) GetDeviceType(token Token, id string) (models.DeviceType, error) {
	return cached(this, "device-type."+id, func() (result models.DeviceType, err error) {
		return result, this.get(token, "/device-types/"+url.QueryEscape(id), &result)
	}, func(dt models.DeviceType) bool { return dt.Id != "" })
}

func (this *CachedRepository) GetDeviceGroup(token Token, id string) (models.DeviceGroup, error) {
	return cached(this, "device-group."+id, func() (result models.DeviceGroup, err error) {
		return result, this.get(token, "/device-groups/"+url.QueryEscape(id), &result)
	}, func(g models.DeviceGroup) bool { return g.Id != "" })
}

// GetService reads a service from the cache or from the device type of the device.
func (this *CachedRepository) GetService(token Token, device models.Device, id string) (models.Service, error) {
	service, err := cache.Get[models.Service](this.cache, "service."+id, func(s models.Service) error {
		if s.Id == "" {
			return errors.New("invalid service loaded from cache")
		}
		return nil
	})
	if err == nil {
		return service, nil
	}
	deviceType, err := this.GetDeviceType(token, device.DeviceTypeId)
	if err != nil {
		return service, err
	}
	for _, s := range deviceType.Services {
		if s.Id == id {
			_ = this.cache.Set("service."+s.Id, s, this.timeout)
			return s, nil
		}
	}
	return service, fmt.Errorf("service %v not found in device type %v", id, device.DeviceTypeId)
}
