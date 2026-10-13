# Offene Punkte außerhalb dieses Repos

Sammelstelle für Aufgaben und Fragen, die andere Repositories, den Betrieb oder die Plattformkonfiguration betreffen. Sie werden nach Abschluss aller Meilensteine gemeinsam durchgegangen. Bis dahin kommen neue Punkte hier dazu, nicht verstreut in `PLAN.md`.

Jeder Punkt nennt seinen Anlass (Anforderungs-ID oder Meilenstein) und ist entweder eine **Aufgabe** (was dort zu tun ist) oder eine **Frage** (was zu klären ist, bevor jemand handelt). Erledigte Punkte werden abgehakt, nicht gelöscht, bis die Liste als Ganzes durch ist.

## Routing und Zugriff

### Kong, Ladon

- [ ] **Aufgabe:** Eine Route für die neue API plus `deprecated/`; die heutigen Routen auf `deprecated/` umleiten (API-08, M7), mit Pfad-Umschreibung und ohne `strip_path` der alten Dienste: `/process/engine` → `/deprecated/engine`, `/process/repository` → `/deprecated/repository`, `/process-io` → `/deprecated/io`, `/process/incidents` → `/deprecated/incidents`, `/process/scheduler` → `/deprecated/scheduler`, `/process/deployment` → `/deprecated/deployment`, `/smart-services/repository` → `/deprecated/smart-services`; `/process-sync` → `/deprecated/process-sync` und `/process-fog-deployment` → `/deprecated/fog-deployment` erst, wenn kein Gateway mehr mit Camunda läuft, weil die Schicht nur Gateways mit der Engine abbildet. Ladon-Regeln der alten Pfade gelten für die neuen Ziele weiter, wenn die Umschreibung nach Ladon greift; sonst sind sie mit umzustellen.
- [ ] **Hinweis (Verhalten):** Unter `deprecated/` gelten die Statuscodes der neuen API (403 statt 401, 404 statt 500 bei unbekannten IDs). Ein Start mit `business_key` legt die Instanz mit dieser ID an; ein zweiter Start mit demselben Schlüssel und denselben Variablen antwortet mit ihr, mit anderen Variablen mit 409, statt eine zweite Instanz anzulegen. Löschen einer laufenden Instanz über die Engine-Routen stoppt sie und behält den Verlauf, wie Camunda es tat. Deployments über `/deprecated/deployment` werden beim Anlegen umgeschrieben (MIG-04); ein Modell mit `mail-send` wird mit 400 abgelehnt. `GET /dependencies` nennt keine Gerätenamen mehr, weil die Engine sie nicht speichert; generische Eventquellen (`selected_generic_event_source`) werden abgelehnt. Releases eines Smart-Service-Designs sind Versionen eines Deployments: ein weiteres Release behält die ID, ältere Versionen heißen `<id>:v<n>`, ein Release ist nur als Ganzes löschbar. Rechte in `permissions_info` kennt die Schicht nur grob (Owner alles, sonst Lesen); `value_label` von Parametern wird nicht gespeichert. Unter `/deprecated/process-sync` heißen Netze nach ihrer ID (die Engine kennt die Namen der Hubs nicht), Optionen vorbereiteter Fog-Deployments sind nicht auf die Geräte des Gateways eingeschränkt, `POST /sync/deployments` tut nichts mehr.
- [ ] **Aufgabe:** Den internen Port (`INTERNAL_PORT`, Vorgabe 8081) nicht routen. Er trägt `/metrics` und die HTTP-Antworten von Geräten (`/responses`, `/errors`) (M3).
- [ ] **Aufgabe:** WebSocket-Upgrade für `/namespaces/*/instances/*/live` durchreichen (DBG-01, DBG-08, M4).
- [ ] **Aufgabe:** Routen und Ladon-Regeln für die Pfade aus M5: `/namespaces/*/modules[/*]`, `/namespaces/*/deployments/*/parameters`, `/namespaces/*/instances/*/parameters`, `/namespaces/*/instances/*/maintenance-procedures[/...]`, `DELETE /namespaces/*/instances/*/variables/*`.
- [ ] **Aufgabe:** Ladon-Regeln für die Admin-Endpunkte `/users` sowie `PUT` und `DELETE /namespaces/{ns}` (SEC-14, NS-02, M2) und für die Debug-Pfade `debug`, `live`, `history`, `event-evaluations`, `variables/{name}` unter `/namespaces/*/instances/*` (M4).
- [ ] **Aufgabe:** Tote Routen `/api-aggregator` und `/eventmanager` entfernen.

### Keycloak

- [ ] **Aufgabe:** Der Client der Engine braucht die Realm-Rolle `admin` (permissions-v2) und die Berechtigung für Token Exchange. Seit der Owner-Prüfung über `CheckPermission` ist Token Exchange Pflicht, sobald permissions-v2 konfiguriert ist; daneben nutzen ihn Gerätekommandos und die Event-Rekonstruktion (SEC-03, SEC-06, SEC-11).

## Plattformdienste

### permissions-v2

- [ ] **Frage:** Lässt sich der Entzug des Owner-Rechts in permissions-v2 selbst verhindern, z. B. über eine Topic-Option? Die Engine reagiert heute auf das Rechte-Signal über Kafka und stellt das Recht wieder her (SEC-10, M3). Eine Lösung in permissions-v2 käme auch Geräten und Hubs zugute.
- [ ] **Frage (Betrieb):** Wird `PERMISSIONS_KAFKA_TOPIC` in der Engine gesetzt? Ohne Wert bleibt ein in permissions-v2 entzogenes Owner-Recht dort entzogen; in der Engine behält der Owner den Zugriff (M3).

### user-management

- [ ] **Aufgabe:** Vor dem Löschen eines Nutzers die Vorabprüfung der Engine aufrufen, danach den Löschaufruf (`/users`), statt process-scheduler anzusprechen. Kafka `user` bleibt der Rückfall (SEC-14, M2).

### gin-middleware (`otelx`)

- [ ] **Frage:** `otelx` legt `username` aus dem Token in die Baggage; der OpenTelemetry-Handler von struct-logger schreibt die Baggage in jeden Logeintrag mit Kontext. Damit landet der Nutzername in den Logs aller so instrumentierten Dienste. Ist das gewollt, oder soll `otelx` nur `user_id` setzen (Tracing, nach M3)?

### device-selection, device-repository, marshaller

- [ ] **Aufgabe:** Die Go-Clients nehmen keinen Kontext an, deshalb geht kein Trace-Kontext weiter. Bei device-selection betrifft das den eigenen Client, bei device-repository der Zugriff in `pkg/devices` (bisher aus `external-task-worker/lib`, jetzt im Repo und um einen Kontext erweiterbar), beim marshaller der Client aus `marshaller/lib` (Tracing, nach M3).

### timescale-wrapper, timescale-rule-manager, timescale-tableworker

- [ ] **Frage:** Auf welche Tabellen wendet timescale-rule-manager Aufbewahrungsregeln an (z. B. `retention-one-year`)? Das begrenzt, wie weit die Rekonstruktion von Event-Auswertungen zurückreicht (EVT-12).
- [ ] **Frage:** Landen Importdaten in Timescale? Davon hängt ab, ob Import-Events rekonstruiert werden können; bis dahin zeigt die Engine für sie nur die Korrelationen (EVT-12, M4).
- [ ] **Frage:** Liefert die Auswertung auf Timescale-Werten dasselbe wie auf den Kafka-Nachrichten? Zu prüfen sind Pfade, Umrechnung, Zeitstempel und Listen mit festen Einträgen, die aus den Spalten als Map statt als Liste zurückkommen (EVT-10, M4).
- [ ] **Aufgabe (Abstimmung):** Die Engine leitet die Spaltennamen aus den Service-Ausgaben ab wie timescale-tableworker (Namen mit Punkt verbunden, Listen mit `*` als eine Spalte) und fragt `/queries` mit dem Token des Owners ab. Änderungen an der Spaltenbildung oder am Antwortformat von `/queries` brechen die Event-Rekonstruktion und die Auswertung des aktuellen Werts; beide Repos sollten das wissen (M4).
- [ ] **Frage:** Kommen die vom Gateway weitergeleiteten Geräteevents vollständig in Timescale an, einschließlich der Zuordnung lokaler IDs zu Cloud-IDs (EVT-13, M6)?

### notifier, developer-notifications

- Keine offenen Punkte; beide sind Ziel von Benachrichtigungen und Incident-Meldungen (INC-02).

## Bibliotheken und geteilte Modelle

### external-task-worker, event-worker (Pakete)

- [x] Die Engine importiert seit 2026-10-12 weder aus external-task-worker noch aus event-worker. Die genutzten Teile liegen in `pkg/devices`, `pkg/tasks/devicecommand` und `pkg/events/unmarshal.go`.
- [ ] **Aufgabe:** device-command und live-data-worker importieren weiter aus `external-task-worker/lib`. Das Repo bleibt für sie bestehen, bis sie umgestellt sind; die Engine ist davon nicht mehr betroffen.
- [ ] **Hinweis:** Die Formate von Kommando-Payload und Protokollnachricht sind jetzt zweimal definiert, in `external-task-worker/lib/messages` und in `pkg/tasks/devicecommand/messages.go`. Ändert platform-connector sie, müssen beide Seiten folgen.

### smart-service-module-worker-lib und die Worker-Repos

- [x] Skript-API, Templates und die Handler von `info`, `import`, `export`, `analytics` und `device_repository` sind seit 2026-10-12 im Engine-Repo (`pkg/modules`, `pkg/modules/compat`); `go.mod` nennt die Repos nicht mehr. Die Testfälle der Worker laufen dort als Golden-Tests weiter (`pkg/modules/testdata`).
- [ ] **Aufgabe:** Die Repos archivieren, sobald die Worker-Deployments stillgelegt sind (siehe Stilllegung). Änderungen an ihnen erreichen die Engine nicht mehr.
- [x] Die gefundenen Fehler sind in der Engine behoben, nicht in den Repos: `info` liest nach einem Zusatzfeld, das kein String ist, weiter; `analytics` bricht bei einem Fehler der Modulsuche ab, statt eine zweite Pipeline anzulegen; eine nur aktualisierte Pipeline wird beim Rückgängigmachen nicht gelöscht. `persistData` als Boolean bleibt `false` wie bisher.

### device-repository

- [x] Die Engine nutzt seit 2026-10-12 `device-repository/v3` (v3.4.0).
- [ ] **Aufgabe (device-selection):** `device-selection/v2` (bis v2.0.3) zieht über `pkg/model` noch `device-repository/v2` herein; in `go.mod` der Engine steht es deshalb als indirekte Abhängigkeit. Mit einer device-selection auf `/v3` entfällt es.

### models (`models/go`)

- [ ] **Frage:** Liegen die Typen der Engine dort oder im Engine-Repo? Heute hält das Engine-Repo sie in `pkg/model` (CLI-01).

### platform-connector-lib

- [ ] **Aufgabe:** `task_info` mit Prozess-IDs im Kommando-Umschlag an das neue Modell anpassen (DATA-04).

### smart-service-module-worker-lib

- [ ] **Frage:** Wird die Bibliothek außerhalb der acht Worker importiert? Sie entfällt mit der Integration der Module (M5).

## Konsumenten der API und der Metriken

### web-ui und Prozess-Designer

- [ ] **Aufgabe:** Beim Import eines Modells aus Camunda (Datei oder altes process-model-repository) `POST /namespaces/{ns}/convert` aufrufen und den Bericht anzeigen: umgeschrieben, von Hand zu prüfen, nicht unterstützt (MIG-04, M7).
- [ ] **Aufgabe:** Fog-Deployments über dieselbe API mit `network_id` statt über process-fog-deployment und process-sync; Netzübersicht über `GET /networks` und `GET /networks/{id}` mit Soll- und Ist-Zustand je Ressource; Instanzen eines Gateways sind bis zur ersten Meldung `pending` (DEP-09, SYNC-12, SYNC-13, M6). Instanzen eines Gateways lassen sich aus der Cloud starten, stoppen und löschen, nicht neu starten oder in Variablen und Parametern ändern. Versionen in `GET /networks/{id}` sind große Zahlen (sie beginnen bei der Zeit in Millisekunden) und taugen nur zum Vergleich von Soll und Ist; der Start einer beendeten Instanz erscheint dort nicht mehr.
- [ ] **Aufgabe:** Debug-Ansicht einer Gateway-Instanz: `offline: true` im Debug-Zustand heißt zuletzt gemeldeter Stand, das Gateway ist nicht erreichbar; Variablen, Durchläufe und Verlauf sind dann nicht abrufbar (503) (DBG-15, M6).

- [ ] **Aufgabe:** Umstellung auf die neue API anhand der OpenAPI in `docs/swagger.json` (API-04, API-07), mit Namespaces (NS-09), Update-Dialog mit Strategie (DEP-04), Modellversionen (MOD-05) und Aufbewahrungseinstellungen (HIST-04).
- [ ] **Aufgabe:** Smart-Service-Designer: Warten auf den Abschluss eines Deployments mit `senergy:messageName` (JavaScript, z. B. `done_event`) statt eines `${...}`-Nachrichtennamens; keine Schleifen im Sequenzfluss in `smart-services` (M5). Start- und Prozedur-Dialoge über `.../parameters`, Status über `status`/`status_message`, Module über `/modules`.
- [ ] **Aufgabe:** Deploy-Dialog: Werte einzelner Elemente (Eingaben, Eventvariablen, Timer, Startvorgaben) über `parameters` des Deployments (DEP-01, M5).
- [ ] **Aufgabe:** Der Designer schreibt nur die unterstützte BPMN-Menge (BPMN-18), nur JavaScript (EXPR-01) und das Modellformat der Engine: `senergy:taskType` statt Topics, `senergy:retries` und `senergy:timeout` am Service Task, `senergy:completionStrategy` an Gerätekommandos, Geräte- und Import-Events mit `senergy:*`-Attributen (M1, M3). Ein Designer für alle Namespaces (NS-09).
- [ ] **Aufgabe:** Live- und historisches Debugging (Abschnitt 13, M4): WebSocket `live` mit dem Token als erster Nachricht `{"type":"auth","token":"..."}`, Schließcodes 4000 plus HTTP-Status (4401 heißt: neues Token holen und mit `after` fortsetzen), Deltas nach der Beschreibung von `model.DebugMessage` übernehmen, Schritte über `history`, Event-Rekonstruktion über `event-evaluations`. Genommene Sequenzflüsse stehen jetzt in `element-stats` (DBG-07).
- [ ] **Aufgabe (Betrieb):** Schickt die web-ui das Token als erste Nachricht, braucht die Engine `KEYCLOAK_CERT_URL`, weil das Token dann nicht über Kong läuft (DBG-08, M4).
- [ ] **Aufgabe:** Die Widgets `data-table` und `device-status`, die heute versteckte Deployments erzeugen (`source=generated`), auf die neue API umstellen.

### dashboard

- [ ] **Aufgabe:** Gespeicherte Widget-Typen mit Prozessbezug an die neue API anpassen.

### canary, snowflake-canary

- [ ] **Aufgabe:** Auf den Go-Client `pkg/client` umstellen (CLI-08).

### cost-calculator, opencost-wrapper

- [ ] **Aufgabe:** Die Engine-Pods statt der external-task-worker-Pods abfragen. Die Metriknamen `external_task_worker_task_command_send_count_vec` und `external_task_worker_task_marshalling_latency` bleiben, die Labels auch; `process_definition_id` trägt jetzt die Deployment-ID (TASK-12, M3).

## Migration (M7)

- [ ] **Aufgabe (Betrieb):** Die Migration als einmaligen Job im Cluster laufen lassen: zuerst als Trockenlauf, Bericht prüfen, Entscheidungen treffen (`-owner`, `-camunda-only`), Betroffene laufender Instanzen informieren (MIG-02), dann mit `-apply`. Braucht Lesezugriff auf die MongoDB der alten Dienste (`MIGRATE_MONGO_URL`), die REST-API aller Camunda-Shards mit Basic-Auth (`MIGRATE_CAMUNDA_PASSWORD`) und den Client der Engine mit Admin-Rolle.
- [ ] **Frage:** Wo speichert camunda-engine-wrapper v1.1.0 den Zustand „deaktiviert“ je Deployment (`PUT /v2/deployments/{id}/disabled`)? Er liegt lokal nicht vor und wird bisher nicht übernommen; deaktivierte Deployments laufen nach der Migration wieder.
- [ ] **Frage:** Wie heißt die Collection von process-scheduler in Prod (`process_scheduler` als Datenbank ist bekannt, lokal heißt die Collection `process_schedule`)? Änderbar mit `-collection-scheduler`.

## Stilllegungen

- [ ] **Aufgabe:** smart-service-engine (Camunda), smart-service-repository mit seiner Mongo, die acht Module-Worker und die `watcher-db` stilllegen, wenn M7 die Daten migriert hat (SMS-01).
- [ ] **Aufgabe:** event-worker und event-deployment stilllegen; der Event-Worker ist eine Rolle der Engine (`ROLE=event`, M3). Die Engine importiert event-worker nicht mehr, das Repo kann danach archiviert werden.
- [ ] **Aufgabe:** Kafka-Topic `process-deployment-done` entfernen, sobald event-deployment aus ist; dazu die Altlasten `deployments_v3`, `camunda_incident` und vermutlich `processmodel` prüfen.
- [ ] **Aufgabe:** process-sync, mgw-process-sync-client und process-fog-deployment archivieren, sobald kein Gateway mehr mit Camunda läuft. Bis dahin bedient process-sync die alten Gateways, die Engine nur Gateways mit ihr selbst; beide nutzen getrennte Topics unter `processes/{nid}/` (M6, Migration M7).
- [ ] **Aufgabe:** api-aggregator abschalten.
- [ ] **Aufgabe:** ArgoCD-Apps `process-engine-prod` und `smart-service-engine-prod` ablösen.

## Gateway (M6)

- [ ] **Aufgabe (Abnahme):** Dauertest auf einem arm64-Gateway mit `pkg/soak` über mindestens 72 h, Bericht (CSV) ablegen (LONG-15). Lokal lief er auf amd64 30 min lang; ob Flash-Speicher, langsamere CPU und der echte Broker etwas ändern, zeigt erst das Gateway.
- [ ] **Aufgabe (Betrieb):** Workflow für Prod-Images (`prod.yml` mit Tag-Vergabe wie in den übrigen Repos) anlegen, sobald die Engine veröffentlicht werden soll; bis dahin gibt es nur `dev.yml`. Secrets `DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN` und `CR_PAT` müssen für das Repo verfügbar sein (FOG-02).

### VerneMQ und senergy-platform-connector (`lib/handler/process`)

- [ ] **Aufgabe (Betrieb):** Die Replicas der Engine verbinden sich mit `SYNC_MQTT_BROKER` als Admin-Client: Sie abonnieren `$share/automation-engine/processes/+/state/engine/v1/#` und veröffentlichen auf `processes/{nid}/cmd/engine/v1/...` jedes Netzes. Der Webhook lässt Wildcards und `$share` nur für Admins zu. Jede Replica braucht eine eigene Client-ID (Vorgabe `automation-engine-{hostname}`).
- [ ] **Frage:** Unterstützt der eingesetzte VerneMQ geteilte Subscriptions mit QoS 1 so, dass eine Nachricht genau einer Replica zugestellt wird? Doppelte Zustellung wäre harmlos (das Protokoll erkennt sie), verlorene holt das Gateway nach.

### mgw-device-cloud-connector

- [x] Upstream mit QoS 0 und Puffer nur im Speicher: Das Sync-Protokoll der Engine bestätigt und sendet selbst erneut (M6); der Connector bleibt unverändert MQTT-Relay für `processes/...`.

### mgw-module-repository (`process/Modfile.yaml`)

- [ ] **Aufgabe:** Das Prozessmodul `main/process` (heute v1.5.4 mit sieben Containern: Camunda und Postgres, mgw-process-sync-client, mgw-external-task-worker, event-worker, process-io-worker und seine Postgres) auf einen Container der Engine umstellen: `MODE=fog`, `MQTT_BROKER=tcp://{broker}:1881`, `DEVICE_REPOSITORY_URL=http://{device-repo-mirror}:8080`, `LAST_VALUE_URL=http://{mgw-last-value}:8080`, `NOTIFIER_URL=http://{gateway}/notifications-v2/notifications`, `MQTT_CLIENT_ID` je Gateway eindeutig, ein Volume für `SQLITE_PATH`, optional `STORAGE_BUDGET`. Abhängigkeiten bleiben Mirror, cloud-proxy, Broker und mgw-last-value (M6).
- [ ] **Hinweis:** Lokale Topics nutzen die `LocalId` aus device-repository unverändert. Setzt mgw-device-cloud-connector `LDH_ID_PREFIX`, passen sie nicht zu den Topics der Connectoren; das gilt heute für mgw-external-task-worker und den event-worker genauso.
- [ ] **Frage:** Der cloud-proxy authentifiziert mit dem Zertifikat des Netzes. Die Engine setzt in Benachrichtigungen die `userId` des Owners der Instanz; weist notifier eine `userId` zurück, die nicht dem Nutzer des Zertifikats entspricht? Warnungen zum Speicherbudget gehen ohne `userId` und landen beim Nutzer des Netzes.
