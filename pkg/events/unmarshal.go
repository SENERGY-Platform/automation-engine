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

package events

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/converter/lib/converter"
	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	marshallerconfig "github.com/SENERGY-Platform/marshaller/lib/config"
	marshallermodel "github.com/SENERGY-Platform/marshaller/lib/marshaller/model"
	v2 "github.com/SENERGY-Platform/marshaller/lib/marshaller/v2"
	"github.com/SENERGY-Platform/models/go/models"
	"github.com/SENERGY-Platform/service-commons/pkg/cache"
	"github.com/SENERGY-Platform/service-commons/pkg/signal"
)

// MessageDesc is what the unmarshalling needs of an event description and the message it reads.
type MessageDesc struct {
	Service          models.Service
	CharacteristicId string
	FunctionId       string
	AspectIds        []string
	// a path set at deploy time wins over the search by function and aspects
	Path    string
	Message map[string]any
}

// DeviceRepo is the read access the unmarshalling needs to device-repository.
type DeviceRepo interface {
	GetCharacteristic(id string) (characteristic models.Characteristic, err error)
	GetConcept(id string) (concept models.Concept, err error)
	GetConceptIdOfFunction(id string) string
	// GetAspectNode is used by the aspect distance sorting of the marshaller, which walks the
	// tree one node at a time
	GetAspectNode(id string) (models.AspectNode, error)
	GetAspectNodes(ids []string) ([]models.AspectNode, error)
}

// Marshaller converts device and import messages into the characteristic of an event as the
// event-worker did (EVT-04).
type Marshaller struct {
	marshaller *v2.Marshaller
	repo       DeviceRepo
}

func NewMarshaller(repo DeviceRepo) (*Marshaller, error) {
	c, err := converter.New()
	if err != nil {
		return nil, err
	}
	return &Marshaller{marshaller: v2.New(marshallerconfig.Config{}, c, repo), repo: repo}, nil
}

func (this *Marshaller) Unmarshal(desc MessageDesc) (value any, err error) {
	path := desc.Path
	if path == "" {
		if path, err = this.path(desc); err != nil {
			return nil, err
		}
	}
	// no protocol is needed, the message is serialized already
	return this.marshaller.Unmarshal(models.Protocol{}, desc.Service, desc.CharacteristicId, path, nil, desc.Message)
}

// path finds the output path of the function; every requested aspect has to be carried by the
// same content variable, and of several paths the one closest to the aspects is used
func (this *Marshaller) path(desc MessageDesc) (string, error) {
	if len(desc.AspectIds) == 0 {
		return "", errors.New("missing aspect id in conditional event description")
	}
	if desc.FunctionId == "" {
		return "", errors.New("missing function id in conditional event description")
	}
	aspects, err := this.repo.GetAspectNodes(desc.AspectIds)
	if err != nil {
		return "", err
	}
	paths := this.marshaller.GetOutputPaths(desc.Service, desc.FunctionId, aspects)
	if len(paths) > 1 {
		if paths, err = this.marshaller.SortPathsByAspectDistance(this.repo, desc.Service, aspects, paths); err != nil {
			return "", err
		}
	}
	if len(paths) == 0 {
		return "", errors.New("no output path found for criteria")
	}
	return paths[0], nil
}

// ErrUnavailable marks an evaluation that failed because device-repository could not answer;
// the message is read again instead of notifying the owner, as the event-worker did.
var ErrUnavailable = errors.New("device-repository unavailable")

// CachedDeviceRepo reads characteristics, concepts, functions and aspect nodes with a client
// token and caches them; cache invalidation signals of service-commons clear the entries.
type CachedDeviceRepo struct {
	client   devicerepository.Interface
	cache    *cache.Cache
	duration time.Duration
}

func NewCachedDeviceRepo(url string, token auth.TokenSource, duration time.Duration) (*CachedDeviceRepo, error) {
	c, err := cache.New(cache.Config{
		CacheInvalidationSignalHooks: map[cache.Signal]cache.ToKey{
			signal.Known.CacheInvalidationAll:            nil,
			signal.Known.ConceptCacheInvalidation:        func(value string) string { return "concept." + value },
			signal.Known.CharacteristicCacheInvalidation: func(value string) string { return "characteristics." + value },
			signal.Known.FunctionCacheInvalidation:       func(value string) string { return "functions." + value },
			// an aspect corresponds to several aspect nodes
			signal.Known.AspectCacheInvalidation: nil,
		},
	})
	if err != nil {
		return nil, err
	}
	return &CachedDeviceRepo{client: devicerepository.NewClient(url, token), cache: c, duration: duration}, nil
}

func cached[T any](this *CachedDeviceRepo, key string, get func() (T, error, int), valid func(T) bool) (T, error) {
	return cache.Use(this.cache, key, func() (T, error) {
		result, err, code := get()
		// the client reports transport and decoding failures as internal error as well
		if err != nil && code >= 500 {
			err = fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return result, err
	}, func(value T) error {
		if !valid(value) {
			return errors.New("invalid " + key + " returned from cache")
		}
		return nil
	}, this.duration)
}

func (this *CachedDeviceRepo) GetCharacteristic(id string) (models.Characteristic, error) {
	return cached(this, "characteristics."+id, func() (models.Characteristic, error, int) { return this.client.GetCharacteristic(id) },
		func(c models.Characteristic) bool { return c.Id != "" })
}

// GetConcept reads the concept with characteristic ids and conversions, which is what the
// marshaller needs, not with the characteristics themselves
func (this *CachedDeviceRepo) GetConcept(id string) (models.Concept, error) {
	return cached(this, "concept."+id, func() (models.Concept, error, int) { return this.client.GetConceptWithoutCharacteristics(id) },
		func(c models.Concept) bool { return c.Id != "" })
}

func (this *CachedDeviceRepo) GetFunction(id string) (models.Function, error) {
	return cached(this, "functions."+id, func() (models.Function, error, int) { return this.client.GetFunction(id) },
		func(f models.Function) bool { return f.Id != "" })
}

// GetConceptIdOfFunction is empty if the function cannot be read; the marshaller then reports
// the missing conversion
func (this *CachedDeviceRepo) GetConceptIdOfFunction(id string) string {
	function, err := this.GetFunction(id)
	if err != nil {
		return ""
	}
	return function.ConceptId
}

func (this *CachedDeviceRepo) GetAspectNode(id string) (models.AspectNode, error) {
	return cached(this, "aspect-nodes."+id, func() (models.AspectNode, error, int) { return this.client.GetAspectNode(id) },
		func(n models.AspectNode) bool { return n.Id != "" })
}

// GetAspectNodes reads the aspect nodes of several ids in one request; every id has to resolve,
// because criteria with an unknown aspect cannot be answered
func (this *CachedDeviceRepo) GetAspectNodes(ids []string) ([]models.AspectNode, error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 {
		return []models.AspectNode{}, nil
	}
	// a key of its own, a single node is cached under its id
	return cached(this, "aspect-node-list."+strings.Join(ids, ","), func() ([]models.AspectNode, error, int) {
		result, _, err, code := this.client.ListAspectNodes(devicerepomodel.AspectListOptions{Ids: ids})
		if err == nil && len(result) != len(ids) {
			missing := []string{}
			for _, id := range ids {
				if !marshallermodel.ContainsAspectNode(result, id) {
					missing = append(missing, id)
				}
			}
			err = fmt.Errorf("unknown aspect nodes: %v", strings.Join(missing, ", "))
		}
		return result, err, code
	}, func(nodes []models.AspectNode) bool { return len(nodes) == len(ids) })
}
