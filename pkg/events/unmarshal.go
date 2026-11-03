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
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/commons"
	"github.com/SENERGY-Platform/converter/lib/converter"
	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	marshallerconfig "github.com/SENERGY-Platform/marshaller/lib/config"
	v2 "github.com/SENERGY-Platform/marshaller/lib/marshaller/v2"
	"github.com/SENERGY-Platform/models/go/models"
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
	GetAspectNode(ctx context.Context, id string) (models.AspectNode, error)
	GetAspectNodes(ctx context.Context, ids []string) ([]models.AspectNode, error)
}

// Marshaller converts device and import messages into the characteristic of an event as the
// event-worker did (EVT-04).
type Marshaller struct {
	marshaller *v2.Marshaller
	repo       DeviceRepo
}

func NewMarshaller(repo DeviceRepo) (*Marshaller, error) {
	c, err := NewLocalConverter()
	if err != nil {
		return nil, err
	}
	return &Marshaller{marshaller: v2.New(marshallerconfig.Config{}, c, repo), repo: repo}, nil
}

// LocalConverter converts in the process, where the marshaller service calls the converter
// service; it sends no request, so it has no use for the context of the marshaller library.
type LocalConverter struct {
	converter *converter.Converter
}

func NewLocalConverter() (LocalConverter, error) {
	c, err := converter.New()
	return LocalConverter{converter: c}, err
}

func (this LocalConverter) Cast(_ context.Context, in any, from string, to string) (any, error) {
	return this.converter.Cast(in, from, to)
}

func (this LocalConverter) CastWithExtension(_ context.Context, in any, from string, to string, extensions []models.ConverterExtension) (any, error) {
	return this.converter.CastWithExtension(in, from, to, extensions)
}

func (this *Marshaller) Unmarshal(desc MessageDesc) (value any, err error) {
	path := desc.Path
	if path == "" {
		if path, err = this.path(desc); err != nil {
			return nil, err
		}
	}
	// no protocol is needed, the message is serialized already; the evaluation of events has no
	// context yet, so the reads of aspect nodes carry no trace
	return this.marshaller.Unmarshal(context.Background(), models.Protocol{}, desc.Service, desc.CharacteristicId, path, nil, desc.Message)
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
	aspects, err := this.repo.GetAspectNodes(context.Background(), desc.AspectIds)
	if err != nil {
		return "", err
	}
	paths := this.marshaller.GetOutputPaths(desc.Service, desc.FunctionId, aspects)
	if len(paths) > 1 {
		if paths, err = this.marshaller.SortPathsByAspectDistance(context.Background(), this.repo, desc.Service, aspects, paths); err != nil {
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

// CachedDeviceRepo gives the marshaller library characteristics, concepts, functions and aspect
// nodes, read through devicerepository.WithCache with ttl. The library asks for aspect nodes
// with a context; characteristics, concepts and functions it asks for without one, so these
// reads carry no trace.
type CachedDeviceRepo struct {
	// subscribed to the cache invalidation signals by whoever created it
	client devicerepository.Interface
	ttl    time.Duration
}

// NewCachedDeviceRepo reads through client, which brings the token if one is needed.
func NewCachedDeviceRepo(client devicerepository.Interface, ttl time.Duration) *CachedDeviceRepo {
	return &CachedDeviceRepo{client: client, ttl: ttl}
}

// unavailable marks a failure of device-repository itself; the client reports transport and
// decoding failures as internal error as well
func unavailable(err error, code int) error {
	if err != nil && code >= 500 {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return err
}

func (this *CachedDeviceRepo) GetCharacteristic(id string) (models.Characteristic, error) {
	result, err, code := devicerepository.WithCache(this.client, this.ttl).GetCharacteristic(context.Background(), id)
	return result, unavailable(err, code)
}

// GetConcept reads the concept with characteristic ids and conversions, which is what the
// marshaller needs, not with the characteristics themselves
func (this *CachedDeviceRepo) GetConcept(id string) (models.Concept, error) {
	result, err, code := devicerepository.WithCache(this.client, this.ttl).GetConceptWithoutCharacteristics(context.Background(), id)
	return result, unavailable(err, code)
}

func (this *CachedDeviceRepo) GetFunction(id string) (models.Function, error) {
	result, err, code := devicerepository.WithCache(this.client, this.ttl).GetFunction(context.Background(), id)
	return result, unavailable(err, code)
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

func (this *CachedDeviceRepo) GetAspectNode(ctx context.Context, id string) (models.AspectNode, error) {
	result, err, code := devicerepository.WithCache(this.client, this.ttl).GetAspectNode(ctx, id)
	return result, unavailable(err, code)
}

// GetAspectNodes reads the aspect nodes of several ids in one request, each id once; every id
// has to resolve, because criteria with an unknown aspect cannot be answered
func (this *CachedDeviceRepo) GetAspectNodes(ctx context.Context, ids []string) ([]models.AspectNode, error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	result, err, code := commons.AspectNodesByIds(ctx, devicerepository.WithCache(this.client, this.ttl), ids)
	return result, unavailable(err, code)
}
