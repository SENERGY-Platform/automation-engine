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

package modules

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// The check of the resources deployments use, as smart-service-repository checks its releases:
// a process model, flow or import type edited or deleted after a version was deployed is
// recorded on the version as a hint for its owner; nothing else changes (SMS-15).

const (
	reasonChanged = "changed"
	reasonGone    = "gone"
)

// RunResourceChecks checks the resources of all deployment versions every cycle; 0 does nothing.
func (this *Runtime) RunResourceChecks(ctx context.Context, cycle time.Duration) {
	if cycle <= 0 {
		return
	}
	ticker := time.NewTicker(cycle)
	defer ticker.Stop()
	for {
		if err := this.CheckResources(ctx); err != nil && ctx.Err() == nil {
			this.logger.WarnContext(ctx, "resource check incomplete", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// versionToCheck is a deployment version that is current or still has instances
type versionToCheck struct {
	deployment model.Deployment
	version    model.DeploymentVersion
	used       model.UsedResources
	previous   store.ResourceCheck
}

// CheckResources checks every deployment version that is current or still has instances and
// names resources in its module tasks.
func (this *Runtime) CheckResources(ctx context.Context) error {
	var versions []versionToCheck
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		for after := ""; ; {
			page, err := tx.AllDeployments(after, 1000)
			if err != nil {
				return err
			}
			for _, d := range page {
				found, err := this.versionsToCheck(tx, d)
				if err != nil {
					return err
				}
				versions = append(versions, found...)
				after = d.Id
			}
			if len(page) < 1000 {
				return nil
			}
		}
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, v := range versions {
		if err := this.checkVersion(ctx, v); err != nil {
			errs = append(errs, fmt.Errorf("deployment %v version %v: %w", v.deployment.Id, v.version.Version, err))
		}
	}
	return errors.Join(errs...)
}

func (this *Runtime) versionsToCheck(tx *store.Tx, d model.Deployment) (result []versionToCheck, err error) {
	counts, err := tx.InstancesPerVersion(d.Id)
	if err != nil {
		return nil, err
	}
	checks, err := tx.ResourceChecks(d.Id)
	if err != nil {
		return nil, err
	}
	for version := 1; version <= d.Version; version++ {
		if version != d.Version && counts[version] == 0 {
			continue
		}
		v, err := tx.GetDeploymentVersion(d.Id, version)
		if errors.Is(err, model.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		process, _, err := bpmn.Parse(v.Xml)
		if err != nil {
			continue
		}
		bpmn.ApplyParameters(process, v.Parameters)
		used := UsedResources(process)
		if len(used.ProcessModels)+len(used.Flows)+len(used.ImportTypes) == 0 {
			continue
		}
		result = append(result, versionToCheck{deployment: d, version: v, used: used, previous: checks[version]})
	}
	return result, nil
}

// observation is what the check learned about one resource
type observation struct {
	gone       bool
	changeTime time.Time
	hash       string
}

func (this *Runtime) checkVersion(ctx context.Context, v versionToCheck) error {
	deployed := v.version.CreatedAt
	now := this.now().Unix()
	previous := map[string]model.ChangedResource{}
	for _, c := range v.previous.Changed {
		previous[c.Kind+"|"+c.Id] = c
	}
	hashes := maps.Clone(v.previous.Hashes)
	if hashes == nil {
		hashes = map[string]string{}
	}
	changed := []model.ChangedResource{}
	// keep keeps what an earlier check found, when this one learned nothing about the resource
	keep := func(kind string, id string) {
		if c, ok := previous[kind+"|"+id]; ok {
			changed = append(changed, c)
		}
	}
	mark := func(kind string, id string, reason string, at int64) {
		if c, ok := previous[kind+"|"+id]; ok && c.Reason == reason && (reason == reasonGone || kind == model.ChangedImportType) {
			// the first time it was seen
			at = c.At
		}
		changed = append(changed, model.ChangedResource{Kind: kind, Id: id, Reason: reason, At: at})
	}
	var errs []error
	for _, id := range v.used.ProcessModels {
		var m model.Model
		err := this.store.Tx(ctx, func(tx *store.Tx) (err error) { m, err = tx.GetModel(id, false); return err })
		switch {
		case errors.Is(err, model.ErrNotFound):
			mark(model.ChangedProcessModel, id, reasonGone, now)
		case err != nil:
			keep(model.ChangedProcessModel, id)
			errs = append(errs, err)
		case m.UpdatedAt.After(deployed):
			mark(model.ChangedProcessModel, id, reasonChanged, m.UpdatedAt.Unix())
		}
	}
	for _, kind := range []string{model.ChangedFlow, model.ChangedImportType} {
		ids, base := v.used.Flows, this.settings.FlowRepoUrl
		if kind == model.ChangedImportType {
			ids, base = v.used.ImportTypes, this.settings.ImportRepositoryUrl
		}
		for _, id := range ids {
			if base == "" {
				keep(kind, id)
				continue
			}
			o, err := this.observe(ctx, kind, base, id, v.deployment.Owner)
			switch {
			case err != nil:
				keep(kind, id)
				errs = append(errs, err)
			case o.gone:
				mark(kind, id, reasonGone, now)
			case kind == model.ChangedFlow && o.changeTime.After(deployed):
				mark(kind, id, reasonChanged, o.changeTime.Unix())
			case kind == model.ChangedImportType:
				baseline, ok := hashes[id]
				if !ok {
					hashes[id] = o.hash
				} else if baseline != o.hash {
					mark(kind, id, reasonChanged, now)
				}
			}
		}
	}
	slices.SortFunc(changed, func(a, b model.ChangedResource) int {
		return strings.Compare(a.Kind+"|"+a.Id, b.Kind+"|"+b.Id)
	})
	if slices.Equal(changed, v.previous.Changed) && maps.Equal(hashes, v.previous.Hashes) && !v.previous.CheckedAt.IsZero() {
		return errors.Join(errs...)
	}
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		return tx.SetResourceCheck(store.ResourceCheck{DeploymentId: v.deployment.Id, Version: v.version.Version, Changed: changed, Hashes: hashes, CheckedAt: this.now()})
	})
	return errors.Join(append(errs, err)...)
}

// observe reads a flow or an import type with the token of the owner. The repositories check
// the right before the existence, and a deleted resource loses its rights, so 401 and 403 are
// asked again with the token of the engine: 404 then means gone.
func (this *Runtime) observe(ctx context.Context, kind string, base string, id string, owner string) (result observation, err error) {
	path := "/flow/"
	if kind == model.ChangedImportType {
		path = "/import-types/"
	}
	target := strings.TrimRight(base, "/") + path + url.PathEscape(id)
	token, err := this.token(owner)
	if err != nil {
		return result, err
	}
	body, status, err := this.read(ctx, target, token)
	if err == nil && (status == http.StatusUnauthorized || status == http.StatusForbidden) && this.adminToken != nil {
		if token, err = this.adminToken(); err != nil {
			return result, err
		}
		body, status, err = this.read(ctx, target, token)
	}
	switch {
	case err != nil:
		return result, err
	case status == http.StatusNotFound:
		return observation{gone: true}, nil
	case status != http.StatusOK:
		return result, fmt.Errorf("GET %v: %v", target, status)
	}
	if kind == model.ChangedImportType {
		result.hash, err = importTypeHash(body)
		return result, err
	}
	var flow struct {
		DateUpdated string `json:"dateUpdated"`
	}
	if err = json.Unmarshal(body, &flow); err != nil {
		return result, err
	}
	if flow.DateUpdated != "" {
		if result.changeTime, err = time.Parse(time.RFC3339, flow.DateUpdated); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (this *Runtime) read(ctx context.Context, target string, token string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", token)
	resp, err := this.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return body, resp.StatusCode, err
}

// importTypeContent are the fields of an import type that decide what an import does; the hash
// covers only these, so that new fields of the API or a change of owner mark nothing
type importTypeContent struct {
	Name           string                `json:"name"`
	Description    string                `json:"description"`
	Image          string                `json:"image"`
	DefaultRestart bool                  `json:"default_restart"`
	Configs        []importTypeConfig    `json:"configs,omitempty"`
	Output         importContentVariable `json:"output"`
}

type importTypeConfig struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Type         string `json:"type"`
	DefaultValue any    `json:"default_value"`
}

type importContentVariable struct {
	Name                string                  `json:"name"`
	Type                string                  `json:"type"`
	CharacteristicId    string                  `json:"characteristic_id"`
	SubContentVariables []importContentVariable `json:"sub_content_variables,omitempty"`
	UseAsTag            bool                    `json:"use_as_tag"`
	FunctionId          string                  `json:"function_id,omitempty"`
	// only read: folded into AspectIds
	AspectId  string   `json:"aspect_id,omitempty"`
	AspectIds []string `json:"aspect_ids,omitempty"`
}

func (this *importContentVariable) normalize() {
	if len(this.AspectIds) == 0 && this.AspectId != "" {
		this.AspectIds = []string{this.AspectId}
	}
	this.AspectId = ""
	for i := range this.SubContentVariables {
		this.SubContentVariables[i].normalize()
	}
}

func importTypeHash(body []byte) (string, error) {
	var content importTypeContent
	if err := json.Unmarshal(body, &content); err != nil {
		return "", err
	}
	content.Output.normalize()
	normalized, err := json.Marshal(content)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:]), nil
}
