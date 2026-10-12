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
	"time"
)

type TokenState string

const (
	// the token is about to execute its element
	TokenReady TokenState = "ready"
	// the token waits for a timer, a message or a job
	TokenWaiting TokenState = "waiting"
	// the token waits at a joining gateway
	TokenJoining TokenState = "joining"
)

// Token marks an active position in a run; it is deleted when it ends (LONG-02).
type Token struct {
	Id         string
	InstanceId string
	Run        int
	ElementId  string
	State      TokenState
	ArrivedVia string
	Locals     map[string]any
	CreatedAt  time.Time
}

func (this *Tx) SaveToken(t Token) error {
	_, err := this.exec("INSERT INTO tokens (id, instance_id, run, element_id, state, arrived_via, locals, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?) "+
		"ON CONFLICT (id) DO UPDATE SET element_id = excluded.element_id, state = excluded.state, arrived_via = excluded.arrived_via, locals = excluded.locals",
		t.Id, t.InstanceId, t.Run, t.ElementId, string(t.State), t.ArrivedVia, toJson(t.Locals), millis(t.CreatedAt))
	return err
}

func (this *Tx) DeleteToken(id string) error {
	_, err := this.exec("DELETE FROM tokens WHERE id = ?", id)
	return err
}

func (this *Tx) ListTokens(instanceId string) (result []Token, err error) {
	rows, err := this.query("SELECT id, instance_id, run, element_id, state, arrived_via, locals, created_at FROM tokens WHERE instance_id = ? ORDER BY created_at, id", instanceId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t Token
		var state, locals string
		var created int64
		if err = rows.Scan(&t.Id, &t.InstanceId, &t.Run, &t.ElementId, &state, &t.ArrivedVia, &locals, &created); err != nil {
			return nil, err
		}
		t.State = TokenState(state)
		t.Locals = fromJson[map[string]any](locals)
		t.CreatedAt = fromMillis(created)
		result = append(result, t)
	}
	return result, rows.Err()
}

// ClearRuntime removes all runtime state of an instance, e.g. when its run ends.
func (this *Tx) ClearRuntime(instanceId string) error {
	for _, table := range []string{"jobs", "subscriptions", "tokens"} {
		if _, err := this.exec("DELETE FROM "+table+" WHERE instance_id = ?", instanceId); err != nil {
			return err
		}
	}
	return nil
}

const (
	SubscriptionMessage = "message"
	SubscriptionTimer   = "timer"
)

// Subscription waits for a message or a timer on behalf of a token.
type Subscription struct {
	Id          string
	InstanceId  string
	Run         int
	TokenId     string
	ElementId   string
	Kind        string
	MessageName string
	DueAt       *time.Time
	// subscriptions of an event-based gateway share a group; the first trigger cancels the others
	GroupId    string
	Definition string
	CreatedAt  time.Time
	// from the instance
	Owner     string
	Namespace string
}

func (this *Tx) InsertSubscription(s Subscription) error {
	_, err := this.exec("INSERT INTO subscriptions (id, instance_id, run, token_id, element_id, kind, message_name, due_at, group_id, definition, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		s.Id, s.InstanceId, s.Run, s.TokenId, s.ElementId, s.Kind, s.MessageName, nullMillis(s.DueAt), s.GroupId, s.Definition, millis(s.CreatedAt))
	return err
}

func (this *Tx) DeleteSubscriptionsOfToken(tokenId string) error {
	_, err := this.exec("DELETE FROM subscriptions WHERE token_id = ?", tokenId)
	return err
}

const subscriptionSelect = "SELECT s.id, s.instance_id, s.run, s.token_id, s.element_id, s.kind, s.message_name, s.due_at, s.group_id, s.definition, s.created_at, i.owner, i.namespace FROM subscriptions s JOIN instances i ON i.id = s.instance_id"

func scanSubscriptions(rows *sql.Rows, err error) (result []Subscription, _ error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s Subscription
		var due sql.NullInt64
		var created int64
		if err = rows.Scan(&s.Id, &s.InstanceId, &s.Run, &s.TokenId, &s.ElementId, &s.Kind, &s.MessageName, &due, &s.GroupId, &s.Definition, &created, &s.Owner, &s.Namespace); err != nil {
			return nil, err
		}
		s.DueAt = fromNullMillis(due)
		s.CreatedAt = fromMillis(created)
		result = append(result, s)
	}
	return result, rows.Err()
}

func (this *Tx) GetSubscription(id string) (*Subscription, error) {
	result, err := scanSubscriptions(this.query(subscriptionSelect+" WHERE s.id = ?", id))
	if err != nil || len(result) == 0 {
		return nil, err
	}
	return &result[0], nil
}

func (this *Tx) ListSubscriptions(instanceId string) ([]Subscription, error) {
	return scanSubscriptions(this.query(subscriptionSelect+" WHERE s.instance_id = ? ORDER BY s.created_at, s.id", instanceId))
}

// MessageSubscriptions finds waiting message events, optionally of one instance or owner.
func (this *Tx) MessageSubscriptions(messageName string, instanceId string, owner string) ([]Subscription, error) {
	w := &where{}
	w.add("s.kind = ?", SubscriptionMessage)
	w.add("s.message_name = ?", messageName)
	if instanceId != "" {
		w.add("s.instance_id = ?", instanceId)
	}
	if owner != "" {
		w.add("i.owner = ?", owner)
	}
	return scanSubscriptions(this.query(subscriptionSelect+w.String()+" ORDER BY s.created_at, s.id", w.args...))
}

func (this *Tx) DueTimers(now time.Time, limit int) ([]Subscription, error) {
	return scanSubscriptions(this.query(subscriptionSelect+" WHERE s.kind = ? AND s.due_at <= ? ORDER BY s.due_at LIMIT ?", SubscriptionTimer, millis(now), limit))
}

func (this *Tx) NextTimerDue() (*time.Time, error) {
	var due sql.NullInt64
	err := this.queryRow("SELECT MIN(due_at) FROM subscriptions WHERE kind = ?", SubscriptionTimer).Scan(&due)
	return fromNullMillis(due), err
}

const (
	// waits for execution
	JobPending = "pending"
	// claimed by an engine replica until lock_until
	JobRunning = "running"
	// the task type waits asynchronously for an answer until lock_until (TASK-00)
	JobWaiting = "waiting"
)

// Job executes a service task outside the transaction of the instance.
type Job struct {
	Id         string
	InstanceId string
	Run        int
	TokenId    string
	ElementId  string
	TaskType   string
	Input      map[string]any
	State      string
	Attempt    int
	Retries    int
	Timeout    time.Duration
	DueAt      time.Time
	LockOwner  string
	LockUntil  time.Time
	LastError  string
	CreatedAt  time.Time
}

const jobColumns = "id, instance_id, run, token_id, element_id, task_type, input, state, attempt, retries, timeout_ms, due_at, lock_owner, lock_until, last_error, created_at"

func scanJob(row interface{ Scan(...any) error }) (j Job, err error) {
	var input string
	var timeout, due, lockUntil, created int64
	err = row.Scan(&j.Id, &j.InstanceId, &j.Run, &j.TokenId, &j.ElementId, &j.TaskType, &input, &j.State, &j.Attempt, &j.Retries, &timeout, &due, &j.LockOwner, &lockUntil, &j.LastError, &created)
	j.Input = fromJson[map[string]any](input)
	j.Timeout = time.Duration(timeout) * time.Millisecond
	j.DueAt = fromMillis(due)
	j.LockUntil = fromMillis(lockUntil)
	j.CreatedAt = fromMillis(created)
	return j, err
}

func (this *Tx) SaveJob(j Job) error {
	_, err := this.exec("INSERT INTO jobs ("+jobColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) "+
		"ON CONFLICT (id) DO UPDATE SET state = excluded.state, attempt = excluded.attempt, due_at = excluded.due_at, lock_owner = excluded.lock_owner, lock_until = excluded.lock_until, last_error = excluded.last_error",
		j.Id, j.InstanceId, j.Run, j.TokenId, j.ElementId, j.TaskType, toJson(j.Input), j.State, j.Attempt, j.Retries, j.Timeout.Milliseconds(), millis(j.DueAt), j.LockOwner, millis(j.LockUntil), j.LastError, millis(j.CreatedAt))
	return err
}

func (this *Tx) GetJob(id string) (*Job, error) {
	j, err := scanJob(this.queryRow("SELECT "+jobColumns+" FROM jobs WHERE id = ?", id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (this *Tx) DeleteJob(id string) error {
	_, err := this.exec("DELETE FROM jobs WHERE id = ?", id)
	return err
}

func (this *Tx) CountJobs(instanceId string) (int, error) {
	return this.count("SELECT COUNT(*) FROM jobs WHERE instance_id = ?", instanceId)
}

// JobCandidates lists pending jobs that are due and claimed jobs whose lock expired.
func (this *Tx) JobCandidates(now time.Time, limit int) (result []Job, err error) {
	rows, err := this.query("SELECT "+jobColumns+" FROM jobs WHERE (state = ? AND due_at <= ?) OR (state <> ? AND lock_until <= ?) ORDER BY due_at LIMIT ?",
		JobPending, millis(now), JobPending, millis(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, j)
	}
	return result, rows.Err()
}

func (this *Tx) NextJobDue() (*time.Time, error) {
	var due sql.NullInt64
	err := this.queryRow("SELECT MIN(CASE WHEN state = ? THEN due_at ELSE lock_until END) FROM jobs", JobPending).Scan(&due)
	return fromNullMillis(due), err
}

// InsertReceipt records that a message has been correlated with a target and reports
// whether it is new (EVT-05).
func (this *Tx) InsertReceipt(messageId string, target string, at time.Time) (bool, error) {
	result, err := this.exec("INSERT INTO message_receipts (message_id, target, received_at) VALUES (?, ?, ?) ON CONFLICT (message_id, target) DO NOTHING", messageId, target, millis(at))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (this *Tx) HasReceipt(messageId string, target string) (bool, error) {
	n, err := this.count("SELECT COUNT(*) FROM message_receipts WHERE message_id = ? AND target = ?", messageId, target)
	return n > 0, err
}

func (this *Tx) PurgeReceipts(before time.Time) (int64, error) {
	result, err := this.exec("DELETE FROM message_receipts WHERE received_at < ?", millis(before))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
