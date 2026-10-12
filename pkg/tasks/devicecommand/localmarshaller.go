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

package devicecommand

// Marshalling in the process, for gateways without marshaller service (FOG-01); following
// mgw-external-task-worker v1.2.2 pkg/marshaller. Commands of payload version 3 use marshaller
// v2 with the characteristics and aspect nodes of the device-repository mirror; older commands
// use marshaller v1, which needs all concepts, characteristics and functions at once.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/converter/lib/converter"
	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	marshallerconfig "github.com/SENERGY-Platform/marshaller/lib/config"
	v1 "github.com/SENERGY-Platform/marshaller/lib/marshaller"
	marshallermodel "github.com/SENERGY-Platform/marshaller/lib/marshaller/model"
	v2 "github.com/SENERGY-Platform/marshaller/lib/marshaller/v2"
	"github.com/SENERGY-Platform/models/go/models"
)

type localMarshaller struct {
	v1      *v1.Marshaller
	v2      *v2.Marshaller
	aspects *events.CachedDeviceRepo
}

// NewLocalMarshaller marshals with the device-repository mirror at repositoryUrl; the concepts
// of marshaller v1 are loaded on first use and refreshed at the interval.
func NewLocalMarshaller(ctx context.Context, repositoryUrl string, cacheDuration time.Duration, refresh time.Duration, logger *slog.Logger) (Marshaller, error) {
	c, err := converter.New()
	if err != nil {
		return nil, err
	}
	// the mirror needs no token
	repo, err := events.NewCachedDeviceRepo(repositoryUrl, nil, cacheDuration)
	if err != nil {
		return nil, err
	}
	concepts := &conceptRepo{client: devicerepository.NewClient(repositoryUrl, nil), refresh: refresh, logger: logger}
	return &localMarshaller{v1: v1.New(c, concepts, aspectsOnly{repo}), v2: v2.New(marshallerconfig.Config{}, c, repo), aspects: repo}, nil
}

func (this *localMarshaller) MarshalV2(service models.Service, protocol models.Protocol, data []MarshallingV2RequestData) (map[string]string, error) {
	return this.v2.Marshal(protocol, service, data)
}

// UnmarshalV2 finds the output path like the marshaller service when the request names none:
// the paths of the function and aspects, the closest to the aspects first
func (this *localMarshaller) UnmarshalV2(request UnmarshallingV2Request) (any, error) {
	if request.Path == "" {
		aspects := request.GetAspectNodes()
		for _, id := range request.GetAspectNodeIds() {
			if id == "" || marshallermodel.ContainsAspectNode(aspects, id) {
				continue
			}
			node, err := this.aspects.GetAspectNode(id)
			if err != nil {
				return nil, err
			}
			aspects = append(aspects, node)
		}
		paths := this.v2.GetOutputPaths(request.Service, request.FunctionId, aspects)
		if len(paths) > 1 {
			var err error
			if paths, err = this.v2.SortPathsByAspectDistance(this.aspects, request.Service, aspects, paths); err != nil {
				return nil, err
			}
		}
		if len(paths) == 0 {
			return nil, errors.New("no output path found for criteria")
		}
		request.Path = paths[0]
	}
	return this.v2.Unmarshal(request.Protocol, request.Service, request.CharacteristicId, request.Path, request.Message, nil)
}

func (this *localMarshaller) MarshalFromServiceAndProtocol(characteristicId string, service models.Service, protocol models.Protocol, data any, configurables []Configurable) (map[string]string, error) {
	return this.v1.MarshalInputs(protocol, service, data, characteristicId, nil, configurables...)
}

func (this *localMarshaller) UnmarshalFromServiceAndProtocol(characteristicId string, service models.Service, protocol models.Protocol, message map[string]string, hints []string) (any, error) {
	return this.v1.UnmarshalOutputs(protocol, service, message, characteristicId, nil, hints...)
}

// aspectsOnly is what marshaller v1 asks of the device repository; device types it reads only
// for requests this engine does not send
type aspectsOnly struct {
	repo *events.CachedDeviceRepo
}

func (this aspectsOnly) GetDeviceType(id string) (models.DeviceType, error, int) {
	return models.DeviceType{}, errors.New("not available on gateways"), 500
}

func (this aspectsOnly) GetAspectNode(id string) (models.AspectNode, error) {
	return this.repo.GetAspectNode(id)
}

// conceptRepo holds all concepts, characteristics and functions, as marshaller v1 needs them
type conceptRepo struct {
	client  devicerepository.Interface
	refresh time.Duration
	logger  *slog.Logger

	mux                   sync.Mutex
	loadedAt              time.Time
	characteristics       map[string]models.Characteristic
	conceptsOfRoot        map[string][]string
	rootOf                map[string]models.Characteristic
	characteristicsOfFunc map[string][]string
}

// ensure loads on first use and after the refresh interval; a failed refresh keeps the
// previous state
func (this *conceptRepo) ensure() error {
	this.mux.Lock()
	defer this.mux.Unlock()
	if this.characteristics != nil && time.Since(this.loadedAt) < this.refresh {
		return nil
	}
	err := this.load()
	if err != nil && this.characteristics != nil {
		this.logger.Warn("unable to refresh concepts, the previous ones stay", "error", err)
		return nil
	}
	return err
}

func (this *conceptRepo) load() error {
	concepts := []models.Concept{}
	for offset := int64(0); ; offset += 1000 {
		page, _, err, _ := this.client.ListConcepts(devicerepository.ConceptListOptions{Limit: 1000, Offset: offset, SortBy: "name.asc"})
		if err != nil {
			return fmt.Errorf("unable to list concepts: %w", err)
		}
		concepts = append(concepts, page...)
		if len(page) < 1000 {
			break
		}
	}
	functions := []models.Function{}
	for offset := int64(0); ; offset += 100 {
		page, _, err, _ := this.client.ListFunctions(devicerepository.FunctionListOptions{Limit: 100, Offset: offset, SortBy: "name.asc"})
		if err != nil {
			return fmt.Errorf("unable to list functions: %w", err)
		}
		functions = append(functions, page...)
		if len(page) < 100 {
			break
		}
	}
	characteristics := map[string]models.Characteristic{}
	conceptsOfRoot := map[string][]string{}
	rootOf := map[string]models.Characteristic{}
	characteristicIdsOfConcept := map[string][]string{}
	for _, concept := range concepts {
		for _, id := range concept.CharacteristicIds {
			characteristic, err, _ := this.client.GetCharacteristic(id)
			if err != nil {
				return fmt.Errorf("unable to read characteristic %v: %w", id, err)
			}
			characteristics[id] = characteristic
			conceptsOfRoot[id] = append(conceptsOfRoot[id], concept.Id)
			for _, descendent := range descendents(characteristic) {
				rootOf[descendent.Id] = characteristic
				if descendent.Id != id {
					characteristics[descendent.Id] = descendent
				}
			}
		}
		characteristicIdsOfConcept[concept.Id] = concept.CharacteristicIds
	}
	characteristicsOfFunc := map[string][]string{}
	for _, f := range functions {
		if ids, ok := characteristicIdsOfConcept[f.ConceptId]; ok {
			characteristicsOfFunc[f.Id] = ids
		}
	}
	this.characteristics, this.conceptsOfRoot, this.rootOf, this.characteristicsOfFunc = characteristics, conceptsOfRoot, rootOf, characteristicsOfFunc
	this.loadedAt = time.Now()
	return nil
}

func descendents(c models.Characteristic) []models.Characteristic {
	result := []models.Characteristic{c}
	for _, child := range c.SubCharacteristics {
		result = append(result, descendents(child)...)
	}
	return result
}

func (this *conceptRepo) GetConceptsOfCharacteristic(characteristicId string) ([]string, error) {
	if err := this.ensure(); err != nil {
		return nil, err
	}
	this.mux.Lock()
	defer this.mux.Unlock()
	concepts, ok := this.conceptsOfRoot[this.rootOf[characteristicId].Id]
	if !ok {
		return nil, errors.New("no concept found for characteristic id " + characteristicId)
	}
	return concepts, nil
}

func (this *conceptRepo) GetCharacteristic(id string) (models.Characteristic, error) {
	if id == "" {
		return marshallermodel.NullCharacteristic, nil
	}
	if err := this.ensure(); err != nil {
		return models.Characteristic{}, err
	}
	this.mux.Lock()
	defer this.mux.Unlock()
	c, ok := this.characteristics[id]
	if !ok {
		return c, errors.New("no characteristic found for id " + id)
	}
	return c, nil
}

func (this *conceptRepo) GetRootCharacteristics(ids []string) (result []string) {
	if this.ensure() != nil {
		return nil
	}
	this.mux.Lock()
	defer this.mux.Unlock()
	for _, id := range ids {
		if root, ok := this.rootOf[id]; ok {
			result = append(result, root.Id)
		}
	}
	return result
}

func (this *conceptRepo) GetCharacteristicsOfFunction(functionId string) ([]string, error) {
	if err := this.ensure(); err != nil {
		return nil, err
	}
	this.mux.Lock()
	defer this.mux.Unlock()
	ids, ok := this.characteristicsOfFunc[functionId]
	if !ok {
		return nil, errors.New("unknown function-id")
	}
	return ids, nil
}
