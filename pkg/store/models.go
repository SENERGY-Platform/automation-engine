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

package store

import (
	"database/sql"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

const modelColumns = "id, namespace, name, description, owner, version, published, publish_description, published_at, created_at, updated_at"

func scanModel(row interface{ Scan(...any) error }) (m model.Model, err error) {
	var published int
	var publishedAt sql.NullInt64
	var created, updated int64
	err = row.Scan(&m.Id, &m.Namespace, &m.Name, &m.Description, &m.Owner, &m.Version, &published, &m.PublishDescription, &publishedAt, &created, &updated)
	m.Published = published == 1
	m.PublishedAt = fromNullMillis(publishedAt)
	m.CreatedAt = fromMillis(created)
	m.UpdatedAt = fromMillis(updated)
	return m, err
}

func (this *Tx) InsertModel(m model.Model) error {
	_, err := this.exec("INSERT INTO models ("+modelColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		m.Id, m.Namespace, m.Name, m.Description, m.Owner, m.Version, boolInt(m.Published), m.PublishDescription, nullMillis(m.PublishedAt), millis(m.CreatedAt), millis(m.UpdatedAt))
	return err
}

func (this *Tx) UpdateModel(m model.Model) error {
	_, err := this.exec("UPDATE models SET name = ?, description = ?, owner = ?, version = ?, published = ?, publish_description = ?, published_at = ?, updated_at = ? WHERE id = ?",
		m.Name, m.Description, m.Owner, m.Version, boolInt(m.Published), m.PublishDescription, nullMillis(m.PublishedAt), millis(m.UpdatedAt), m.Id)
	return err
}

func (this *Tx) GetModel(id string, lock bool) (model.Model, error) {
	m, err := scanModel(this.queryRow("SELECT "+modelColumns+" FROM models WHERE id = ?"+this.forUpdate(lock), id))
	return m, notFound(err, "model %v not found", id)
}

func (this *Tx) DeleteModel(id string) error {
	if _, err := this.exec("UPDATE deployments SET model_deleted = 1 WHERE model_id = ?", id); err != nil {
		return err
	}
	if err := this.queueRemovals("models", "id = ?", id); err != nil {
		return err
	}
	_, err := this.exec("DELETE FROM models WHERE id = ?", id)
	return err
}

func (this *Tx) ListModels(namespace string, options model.ModelListOptions, access Access) (result model.Page[model.Model], err error) {
	w := &where{}
	w.add("namespace = ?", namespace)
	w.access("", access)
	w.search("name", options.Search)
	if len(options.Ids) > 0 {
		w.in("id", options.Ids)
	}
	if options.Owner != "" {
		w.add("owner = ?", options.Owner)
	}
	order, err := orderBy(options.Sort, map[string]string{"name": "LOWER(name)", "created_at": "created_at", "updated_at": "updated_at"}, "LOWER(name) ASC, id ASC")
	if err != nil {
		return result, err
	}
	if result.Total, err = this.count("SELECT COUNT(*) FROM models"+w.String(), w.args...); err != nil {
		return result, err
	}
	limit, limitArgs := limitOffset(options.PageOptions)
	rows, err := this.query("SELECT "+modelColumns+" FROM models"+w.String()+order+limit, append(w.args, limitArgs...)...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	result.Items = []model.Model{}
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, m)
	}
	page := options.PageOptions.Normalized()
	result.Limit, result.Offset = page.Limit, page.Offset
	return result, rows.Err()
}

func (this *Tx) InsertModelVersion(v model.ModelVersion) error {
	_, err := this.exec("INSERT INTO model_versions (model_id, version, xml, svg, issues, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		v.ModelId, v.Version, v.Xml, v.Svg, toJson(v.Issues), millis(v.CreatedAt))
	return err
}

func (this *Tx) GetModelVersion(modelId string, version int) (v model.ModelVersion, err error) {
	var issues string
	var created int64
	err = this.queryRow("SELECT model_id, version, xml, svg, issues, created_at FROM model_versions WHERE model_id = ? AND version = ?", modelId, version).
		Scan(&v.ModelId, &v.Version, &v.Xml, &v.Svg, &issues, &created)
	v.Issues = fromJson[[]model.Issue](issues)
	v.CreatedAt = fromMillis(created)
	return v, notFound(err, "version %v of model %v not found", version, modelId)
}

// ListModelVersions returns versions without XML and SVG, newest first.
func (this *Tx) ListModelVersions(modelId string, page model.PageOptions) (result model.Page[model.ModelVersion], err error) {
	if result.Total, err = this.count("SELECT COUNT(*) FROM model_versions WHERE model_id = ?", modelId); err != nil {
		return result, err
	}
	limit, limitArgs := limitOffset(page)
	rows, err := this.query("SELECT model_id, version, issues, created_at FROM model_versions WHERE model_id = ? ORDER BY version DESC"+limit, append([]any{modelId}, limitArgs...)...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	result.Items = []model.ModelVersion{}
	for rows.Next() {
		var v model.ModelVersion
		var issues string
		var created int64
		if err = rows.Scan(&v.ModelId, &v.Version, &issues, &created); err != nil {
			return result, err
		}
		v.Issues = fromJson[[]model.Issue](issues)
		v.CreatedAt = fromMillis(created)
		result.Items = append(result.Items, v)
	}
	page = page.Normalized()
	result.Limit, result.Offset = page.Limit, page.Offset
	return result, rows.Err()
}
