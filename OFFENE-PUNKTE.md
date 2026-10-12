# Offene Punkte außerhalb dieses Repos

Sammelstelle für Aufgaben und Fragen, die andere Repositories, den Betrieb oder die Plattformkonfiguration betreffen. Sie werden nach Abschluss aller Meilensteine gemeinsam durchgegangen. Bis dahin kommen neue Punkte hier dazu, nicht verstreut in `PLAN.md`.

Jeder Punkt nennt seinen Anlass (Anforderungs-ID oder Meilenstein) und ist entweder eine **Aufgabe** (was dort zu tun ist) oder eine **Frage** (was zu klären ist, bevor jemand handelt). Erledigte Punkte werden abgehakt, nicht gelöscht, bis die Liste als Ganzes durch ist.

## Routing und Zugriff

### Kong, Ladon

- [ ] **Aufgabe:** Eine Route für die neue API plus `deprecated/`; die heutigen Routen `/process/engine`, `/process/deployment`, `/process/repository`, `/process/scheduler`, `/process/incidents`, `/process-io`, `/process-sync`, `/process-fog-deployment`, `/smart-services/repository` auf `deprecated/` umleiten (API-08, M7).
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

- [ ] **Aufgabe:** Die Go-Clients nehmen keinen Kontext an, deshalb geht kein Trace-Kontext weiter. Bei device-selection betrifft das den eigenen Client, bei device-repository und marshaller die Clients aus `external-task-worker/lib` (Tracing, nach M3).

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

- [ ] **Aufgabe:** Einen stabilen Ort für die geteilten Pakete schaffen (CLI-11). Die Engine importiert aus `external-task-worker/lib` die Pakete `messages`, `devicerepository`, `marshaller` und `timescale` sowie aus event-worker `pkg/marshaller`, `devicerepo`, `model` und `configuration`. device-command und live-data-worker importieren ebenfalls aus `external-task-worker/lib`. Ein inkompatibler Bruch dort zeigt sich erst beim nächsten Build der Engine (M3).

### smart-service-module-worker-lib und die Worker-Repos

- [ ] **Aufgabe:** Die Engine bindet Middleware, Skript-API und Templates der Bibliothek sowie die Handler von `info`, `import`, `export`, `analytics` und `device_repository` als Bibliothek ein (M5). Ihr Camunda-Teil, die Gesundheitsprüfung und die Deployments der Worker werden nicht mehr gebraucht. Ein inkompatibler Bruch in den Handlern oder der Skript-API zeigt sich erst beim nächsten Build der Engine; mit CLI-11 ziehen die Pakete an einen stabilen Ort.
- [ ] **Frage:** Sollen die gefundenen Fehler in den Handlern dort behoben werden? `info` bricht beim ersten Zusatzfeld, das kein String ist, die übrigen ab (`break`); `analytics` ignoriert den Fehler der Modulsuche und legt dann eine zweite Pipeline an, und sein Undo löscht eine nur aktualisierte Pipeline (die Engine ruft Undo für bestehende Module nicht mehr auf); `persistData` als Boolean wird stillschweigend `false`.

### device-repository

- [ ] **Frage:** device-repository hat den Modulpfad `/v3` (bis v3.4.0), external-task-worker, die Worker-Bibliothek und die Worker nutzen `/v2` (v2.2.2). Die Engine kann erst wechseln, wenn diese Bibliotheken wechseln.

### models (`models/go`)

- [ ] **Frage:** Liegen die Typen der Engine dort oder im Engine-Repo? Heute hält das Engine-Repo sie in `pkg/model` (CLI-01).

### platform-connector-lib

- [ ] **Aufgabe:** `task_info` mit Prozess-IDs im Kommando-Umschlag an das neue Modell anpassen (DATA-04).

### smart-service-module-worker-lib

- [ ] **Frage:** Wird die Bibliothek außerhalb der acht Worker importiert? Sie entfällt mit der Integration der Module (M5).

## Konsumenten der API und der Metriken

### web-ui und Prozess-Designer

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

## Stilllegungen

- [ ] **Aufgabe:** smart-service-engine (Camunda), smart-service-repository mit seiner Mongo, die acht Module-Worker und die `watcher-db` stilllegen, wenn M7 die Daten migriert hat (SMS-01).
- [ ] **Aufgabe:** event-worker und event-deployment stilllegen; der Event-Worker ist eine Rolle der Engine (`ROLE=event`, M3).
- [ ] **Aufgabe:** Kafka-Topic `process-deployment-done` entfernen, sobald event-deployment aus ist; dazu die Altlasten `deployments_v3`, `camunda_incident` und vermutlich `processmodel` prüfen.
- [ ] **Aufgabe:** process-sync und mgw-process-sync-client archivieren, wenn die Synchronisation in der Engine läuft (M6).
- [ ] **Aufgabe:** api-aggregator abschalten.
- [ ] **Aufgabe:** ArgoCD-Apps `process-engine-prod` und `smart-service-engine-prod` ablösen.

## Gateway (M6)

### mgw-device-cloud-connector

- [ ] **Aufgabe:** Upstream mit QoS 0 und Puffer nur im Speicher reicht für SYNC-02 nicht; bleibt als MQTT-Relay für `processes/...` mit geänderter Zustellung.

### mgw-module-repository (`process/Modfile.yaml`)

- [ ] **Aufgabe:** Das Gateway-Modul auf Engine und Event-Worker ohne eigene Datenbank-Container umstellen.
