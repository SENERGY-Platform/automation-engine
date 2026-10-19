/*
 * Copyright 2026 InfAI (CC SES)
 * Licensed under the Apache License, Version 2.0; see LICENSE in the repository root.
 *
 * Minimal web interface for trying the engine by hand. Every call goes to /api/ (the engine)
 * with the token devauth issues for the selected user; nothing is stored but the selections.
 */
'use strict';

const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];

const SAMPLE = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:camunda="http://camunda.org/schema/1.0/bpmn" id="Definitions_sample" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="m" name="go"/>
  <bpmn:process id="wait_for_message" name="Wait for go" isExecutable="true">
    <bpmn:startEvent id="start">
      <bpmn:extensionElements><camunda:formData><camunda:formField id="level" label="level" type="string" defaultValue="5"/></camunda:formData></bpmn:extensionElements>
    </bpmn:startEvent>
    <bpmn:scriptTask id="double" scriptFormat="javascript" camunda:resultVariable="doubled"><bpmn:script>level * 2</bpmn:script></bpmn:scriptTask>
    <bpmn:intermediateCatchEvent id="wait"><bpmn:messageEventDefinition messageRef="m"/></bpmn:intermediateCatchEvent>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="double"/>
    <bpmn:sequenceFlow id="f2" sourceRef="double" targetRef="wait"/>
    <bpmn:sequenceFlow id="f3" sourceRef="wait" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>
`;

const state = {token: '', namespace: localStorage.getItem('namespace') || 'processes', instance: null, live: null, timer: null};

// --- helpers -----------------------------------------------------------------------------------

function status(text, error = false) {
  const element = $('#status');
  element.textContent = text;
  element.className = error ? 'error' : '';
}

async function api(method, path, body) {
  const options = {method, headers: {Authorization: state.token}};
  if (body !== undefined) {
    options.headers['Content-Type'] = 'application/json';
    options.body = typeof body === 'string' ? body : JSON.stringify(body);
  }
  const response = await fetch('/api' + path, options);
  const text = await response.text();
  let data = text;
  try {
    data = text ? JSON.parse(text) : null;
  } catch (e) { /* not JSON */ }
  if (!response.ok) {
    const message = (data && data.error) || text || response.statusText;
    status(`${method} ${path}: ${response.status} ${message}`, true);
    throw Object.assign(new Error(message), {status: response.status, data});
  }
  return data;
}

const ns = () => `/namespaces/${encodeURIComponent(state.namespace)}`;
const json = (value) => JSON.stringify(value, null, 2);
const time = (value) => value ? new Date(value).toLocaleString() : '';

function parseJson(text, name) {
  if (!text.trim()) return undefined;
  try {
    return JSON.parse(text);
  } catch (e) {
    status(`${name} is not valid JSON: ${e.message}`, true);
    throw e;
  }
}

function cell(row, content) {
  const td = row.insertCell();
  if (content instanceof Node) td.append(content); else td.textContent = content ?? '';
  return td;
}

function button(label, action, title) {
  const element = document.createElement('button');
  element.textContent = label;
  if (title) element.title = title;
  element.addEventListener('click', async (event) => {
    event.preventDefault();
    try {
      await action();
    } catch (e) { /* reported by api() */ }
  });
  return element;
}

function actions(...buttons) {
  const span = document.createElement('span');
  span.className = 'actions';
  span.append(...buttons);
  return span;
}

// --- user and namespace ------------------------------------------------------------------------

async function selectUser() {
  let value = $('#user').value;
  $('#custom-user').hidden = value !== 'custom';
  if (value === 'custom') value = $('#custom-user').value.trim();
  if (!value) return;
  const [user, roles] = value.split('|');
  const response = await fetch(`/auth/token/${encodeURIComponent(user)}${roles ? '?roles=' + roles : ''}`);
  if (!response.ok) {
    status(`devauth: ${response.status}`, true);
    return;
  }
  state.token = (await response.text()).trim();
  localStorage.setItem('user', $('#user').value);
  // nothing of the previous user stays on screen
  closeInstance();
  $('#instance-form').deployment_id.replaceChildren();
  $$('tbody').forEach((body) => body.replaceChildren());
  $$('pre.detail').forEach((pre) => pre.textContent = '');
  status(`signed in as ${user}`);
  await loadNamespaces();
  refresh(currentTab());
}

async function loadNamespaces() {
  const namespaces = await api('GET', '/namespaces');
  const select = $('#namespace');
  select.replaceChildren(...namespaces.map((n) => new Option(n.name, n.name)));
  if (!namespaces.some((n) => n.name === state.namespace) && namespaces.length) state.namespace = namespaces[0].name;
  select.value = state.namespace;
}

// --- tabs --------------------------------------------------------------------------------------

function currentTab() {
  return localStorage.getItem('tab') || 'models';
}

function showTab(name) {
  localStorage.setItem('tab', name);
  $$('.tab').forEach((section) => section.hidden = section.id !== name);
  $$('nav button').forEach((b) => b.classList.toggle('active', b.dataset.tab === name));
  refresh(name);
}

function refresh(name) {
  const loaders = {models: loadModels, deployments: loadDeployments, instances: loadInstances, incidents: loadIncidents};
  if (state.token && loaders[name]) loaders[name]().catch(() => {});
}

// --- models ------------------------------------------------------------------------------------

async function loadModels() {
  const page = await api('GET', `${ns()}/models?limit=100&sort=updated_at.desc`);
  const body = $('#model-list');
  body.replaceChildren();
  for (const m of page.items) {
    const row = body.insertRow();
    cell(row, m.name);
    cell(row, m.id).className = 'id';
    cell(row, m.version);
    cell(row, m.owner);
    cell(row, actions(
      button('Deploy', () => {
        const form = $('#deployment-form');
        form.model_id.value = m.id;
        form.name.value = m.name;
        form.xml.value = '';
        showTab('deployments');
      }),
      button('BPMN', async () => {
        const version = await api('GET', `${ns()}/models/${m.id}/versions/${m.version}`);
        $('#model-form').xml.value = version.xml;
        status(`BPMN of ${m.name} v${m.version} in the editor`);
      }),
      button('Delete', async () => {
        if (!confirm(`Delete model ${m.name}?`)) return;
        await api('DELETE', `${ns()}/models/${m.id}`);
        loadModels();
      }),
    ));
  }
  status(`${page.total} models`);
}

async function createModel(event) {
  event.preventDefault();
  const form = event.target;
  await api('POST', `${ns()}/models`, {name: form.name.value, xml: form.xml.value});
  status('model created');
  loadModels();
}

async function validateModel() {
  const issues = await api('POST', `${ns()}/validate`, {xml: $('#model-form').xml.value});
  status(issues && issues.length ? `${issues.length} issues: ${issues.map((i) => `${i.element_id || ''} ${i.message}`).join('; ')}` : 'valid', issues && issues.length > 0);
}

// --- deployments -------------------------------------------------------------------------------

async function loadDeployments() {
  const page = await api('GET', `${ns()}/deployments?limit=100&sort=updated_at.desc`);
  const body = $('#deployment-list');
  body.replaceChildren();
  const select = $('#instance-form').deployment_id;
  const selected = select.value;
  select.replaceChildren(...page.items.map((d) => new Option(`${d.name} (v${d.version})`, d.id)));
  if (selected) select.value = selected;
  for (const d of page.items) {
    const row = body.insertRow();
    cell(row, d.name);
    cell(row, d.id).className = 'id';
    cell(row, d.version);
    cell(row, d.owner);
    cell(row, d.disabled ? 'switched off' : (d.faulty_since ? 'faulty' : 'on'));
    cell(row, actions(
      button('Start', () => {
        $('#instance-form').deployment_id.value = d.id;
        showTab('instances');
      }),
      button(d.disabled ? 'Switch on' : 'Switch off', async () => {
        await api('PUT', `${ns()}/deployments/${d.id}/disabled`, {disabled: !d.disabled});
        loadDeployments();
      }),
      button('Details', async () => {
        const [version, events] = await Promise.all([
          api('GET', `${ns()}/deployments/${d.id}/versions/${d.version}`),
          api('GET', `${ns()}/deployments/${d.id}/event-descriptions`).catch(() => []),
        ]);
        delete version.xml;
        delete version.svg;
        const detail = $('#deployment-detail');
        detail.hidden = false;
        detail.textContent = json({deployment: d, version, event_descriptions: events});
      }),
      button('Delete', async () => {
        if (!confirm(`Delete deployment ${d.name} with its instances?`)) return;
        await api('DELETE', `${ns()}/deployments/${d.id}`);
        loadDeployments();
      }),
    ));
  }
  status(`${page.total} deployments`);
}

async function createDeployment(event) {
  event.preventDefault();
  const form = event.target;
  const input = {name: form.name.value};
  if (form.model_id.value.trim()) input.model_id = form.model_id.value.trim();
  if (form.xml.value.trim()) input.xml = form.xml.value;
  const selections = parseJson(form.selections.value, 'selections');
  if (selections) input.selections = selections;
  const d = await api('POST', `${ns()}/deployments`, input);
  status(`deployed ${d.name} (${d.id})`);
  loadDeployments();
}

// --- instances ---------------------------------------------------------------------------------

async function loadInstances() {
  if (!$('#instance-form').deployment_id.options.length) await loadDeployments();
  const stateFilter = $('#instance-state').value;
  const page = await api('GET', `${ns()}/instances?limit=100&sort=started_at.desc${stateFilter ? '&state=' + stateFilter : ''}`);
  const body = $('#instance-list');
  body.replaceChildren();
  for (const i of page.items) {
    const row = body.insertRow();
    row.classList.toggle('selected', state.instance === i.id);
    cell(row, i.deployment_name);
    cell(row, i.id).className = 'id';
    cell(row, i.state).className = 'state-' + i.state;
    cell(row, i.status ? `${i.status}${i.status_message ? ': ' + i.status_message : ''}` : (i.end_reason || ''));
    cell(row, i.run);
    cell(row, time(i.started_at));
    cell(row, actions(
      button('Open', () => openInstance(i.id)),
      button('Stop', async () => {
        await api('POST', `${ns()}/instances/${i.id}/stop`);
        loadInstances();
      }),
      button('Restart', async () => {
        await api('POST', `${ns()}/instances/${i.id}/restart`, {});
        loadInstances();
      }),
      button('Delete', async () => {
        if (!confirm('Delete the instance with its history?')) return;
        await api('DELETE', `${ns()}/instances/${i.id}`);
        if (state.instance === i.id) closeInstance();
        loadInstances();
      }),
    ));
  }
  status(`${page.total} instances`);
}

async function startInstance(event) {
  event.preventDefault();
  const form = event.target;
  const input = {deployment_id: form.deployment_id.value, variables: parseJson(form.variables.value, 'variables') || {}};
  if (form.name.value) input.name = form.name.value;
  const i = await api('POST', `${ns()}/instances`, input);
  status(`started ${i.id}`);
  await loadInstances();
  openInstance(i.id);
}

async function showStartParameters() {
  const id = $('#instance-form').deployment_id.value;
  if (!id) return;
  const parameters = await api('GET', `${ns()}/deployments/${id}/start-parameters`);
  const variables = {};
  for (const p of parameters) variables[p.id] = p.default ?? '';
  $('#instance-form').variables.value = json(variables);
  status(`${parameters.length} start parameters`);
}

function openInstance(id) {
  closeLive();
  state.instance = id;
  $('#instance-detail').hidden = false;
  $('#detail-id').textContent = id;
  $$('#instance-list tr').forEach((row) => row.classList.toggle('selected', row.cells[1] && row.cells[1].textContent === id));
  showDetail('instance');
}

function closeInstance() {
  closeLive();
  state.instance = null;
  $('#instance-detail').hidden = true;
}

async function showDetail(kind) {
  const base = `${ns()}/instances/${state.instance}`;
  const paths = {
    instance: base,
    variables: `${base}/variables`,
    activities: `${base}/activities?limit=200`,
    runs: `${base}/runs`,
    debug: `${base}/debug`,
    incidents: `${ns()}/incidents?instance_id=${state.instance}`,
  };
  closeLive();
  const data = await api('GET', paths[kind]);
  $('#detail-output').textContent = json(data);
}

async function sendToInstance(event) {
  event.preventDefault();
  const form = event.target;
  const input = {name: form.name.value, instance_id: state.instance};
  const variables = parseJson(form.variables.value, 'variables');
  if (variables) input.variables = variables;
  const result = await api('POST', '/messages', input);
  status(`${(result.correlations || []).length} correlations`);
  showDetail('instance');
}

// The engine streams a snapshot, then deltas, then the end; webui passes the token as header.
function toggleLive() {
  if (state.live) {
    closeLive();
    return;
  }
  const output = $('#detail-output');
  output.textContent = '';
  const scheme = location.protocol === 'https:' ? 'wss' : 'ws';
  const url = `${scheme}://${location.host}/api${ns()}/instances/${state.instance}/live?access_token=${encodeURIComponent(state.token)}`;
  const socket = new WebSocket(url);
  state.live = socket;
  $('#live-toggle').classList.add('active');
  socket.onmessage = (message) => {
    let data = message.data;
    try {
      data = json(JSON.parse(message.data));
    } catch (e) { /* not JSON */ }
    output.textContent = `${new Date().toLocaleTimeString()}\n${data}\n\n` + output.textContent;
  };
  socket.onclose = (event) => {
    output.textContent = `closed: ${event.code} ${event.reason}\n\n` + output.textContent;
    if (state.live === socket) closeLive();
  };
}

function closeLive() {
  if (state.live) {
    const socket = state.live;
    state.live = null;
    socket.close();
  }
  $('#live-toggle').classList.remove('active');
}

// --- incidents and messages --------------------------------------------------------------------

async function loadIncidents() {
  const page = await api('GET', `${ns()}/incidents?limit=100`);
  const body = $('#incident-list');
  body.replaceChildren();
  for (const i of page.items) {
    const row = body.insertRow();
    cell(row, time(i.time));
    cell(row, i.deployment_id).className = 'id';
    cell(row, button(i.instance_id, () => {
      showTab('instances');
      openInstance(i.instance_id);
    }, 'open the instance'));
    cell(row, i.element_name || i.element_id);
    cell(row, i.message);
  }
  status(`${page.total} incidents`);
}

async function sendMessage(event) {
  event.preventDefault();
  const form = event.target;
  const input = {name: form.name.value, all: form.all.checked};
  if (form.instance_id.value.trim()) input.instance_id = form.instance_id.value.trim();
  const variables = parseJson(form.variables.value, 'variables');
  if (variables) input.variables = variables;
  const result = await api('POST', '/messages', input);
  const output = $('#message-result');
  output.hidden = false;
  output.textContent = json(result);
  status(`${(result.correlations || []).length} correlations`);
}

// --- console -----------------------------------------------------------------------------------

async function sendRequest(event) {
  event.preventDefault();
  const form = event.target;
  const path = form.path.value.startsWith('/') ? form.path.value : '/' + form.path.value;
  const output = $('#console-result');
  try {
    const data = await api(form.method.value, path, form.body.value.trim() ? form.body.value : undefined);
    output.textContent = typeof data === 'string' ? data : json(data);
    status(`${form.method.value} ${path}: ok`);
  } catch (e) {
    output.textContent = typeof e.data === 'string' ? e.data : json(e.data);
  }
}

// --- wiring ------------------------------------------------------------------------------------

function on(selector, event, handler) {
  $(selector).addEventListener(event, async (e) => {
    try {
      await handler(e);
    } catch (err) { /* reported by api() */ }
  });
}

$$('nav button').forEach((b) => b.addEventListener('click', () => showTab(b.dataset.tab)));
$$('[data-refresh]').forEach((b) => b.addEventListener('click', () => refresh(b.dataset.refresh)));
$$('[data-detail]').forEach((b) => b.addEventListener('click', () => showDetail(b.dataset.detail).catch(() => {})));
$$('[data-sample]').forEach((b) => b.addEventListener('click', () => b.form[b.dataset.sample].value = SAMPLE));
$$('input[type=file][data-into]').forEach((input) => input.addEventListener('change', async () => {
  if (input.files.length) input.form[input.dataset.into].value = await input.files[0].text();
}));

on('#user', 'change', selectUser);
on('#custom-user', 'change', selectUser);
on('#namespace', 'change', (e) => {
  state.namespace = e.target.value;
  localStorage.setItem('namespace', state.namespace);
  closeInstance();
  refresh(currentTab());
});
on('#copy-token', 'click', async () => {
  await navigator.clipboard.writeText(state.token);
  status('token copied');
});
on('#model-form', 'submit', createModel);
on('#model-validate', 'click', validateModel);
on('#deployment-form', 'submit', createDeployment);
on('#instance-form', 'submit', startInstance);
on('#start-parameters', 'click', showStartParameters);
on('#instance-state', 'change', loadInstances);
on('#detail-message', 'submit', sendToInstance);
on('#live-toggle', 'click', toggleLive);
on('#message-form', 'submit', sendMessage);
on('#console-form', 'submit', sendRequest);
$('#auto-refresh').addEventListener('change', (e) => {
  clearInterval(state.timer);
  if (e.target.checked) state.timer = setInterval(() => loadInstances().catch(() => {}), 2000);
});

$('#user').value = localStorage.getItem('user') || 'alice';
showTab(currentTab());
selectUser();
