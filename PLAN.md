# Plan: Automation-Engine

Grundlage: `docs/anforderungen.md` (Stand 2026-10-12). Die IDs in Klammern verweisen dorthin.

Die Anforderungen beschreiben ein Programm, nicht eine einzelne Änderung. Sie werden in Meilensteinen umgesetzt, die jeweils für sich lauffähig und getestet sind.

| # | Meilenstein | Kern-IDs | Stand |
|---|---|---|---|
| M1 | Ausführungskern: BPMN-Teilmenge, JS-Sandbox, Token-Ausführung, Timer, Korrelation, Läufe, Verlauf, Namespaces aus Konfiguration, neue REST-API, Go-Client mit Test-Client | BPMN-01–18, EXPR-01–05, DATA-01–03, NS-01–03/06/11 (Konfiguration), MOD-01/03–05, DEP-02/04, RUN-01–12 (ohne 07), HIST-01–03/05, LONG-01–05/08/12/16, EVT-01/02/05, CLI-01/03/04/08/09 | umgesetzt, siehe unten |
| M2 | permissions-v2 (Topics je Namespace, Startrechte, Owner-Schutz), Incident-Behandlung mit Restart und Backoff, Aufbewahrung mit Obergrenzen, Nutzer löschen, Admin-API für Namespaces, Prometheus-Metriken, Token-Provider im Client | NS-02/05/07, SEC-01–14, INC-02–04, HIST-04/06, LONG-03 (Alter), LONG-13/14, MOD-02/07/08, OPS-05, CLI-10 | umgesetzt, siehe unten |
| M3 | Aufgabentypen Gerätekommando, Process-IO, Benachrichtigung, HTTP-Aufruf; Zeitpläne; Event-Worker als Rolle des Binaries, Eventbeschreibungen, Gruppen-Expansion; Vorbereitungs-API; Kafka `user` und Rechte-Signal | TASK-01–12, IO-01–03, BPMN-09/10, RUN-07, DEP-01/05–10, EVT-01–08, NS-04/11 | umgesetzt, siehe unten |
| M4 | Debugging: Outbox, WebSocket-Live-Strom, historisches Debugging, Rekonstruktion von Event-Auswertungen | SYNC-03, DBG-*, EVT-09–13, CLI-05 | offen |
| M5 | Smart-Services-Module, Watcher, Wartungsprozeduren | TASK-20–29, SMS-* | offen |
| M6 | Fog-Betriebsart und Synchronisation | FOG-*, SYNC-*, LONG-06/07/09–11/14/15 | offen |
| M7 | `deprecated/`-Schicht, Migration aus Camunda, Modell-Umschreibung | API-02/03/08, MIG-* | offen |

Außerhalb dieses Repos und deshalb in keinem Meilenstein: Kong-Umleitung (API-08), web-ui (API-04, NS-09), user-management (SEC-14), platform-connector-lib (DATA-04), cost-calculator (TASK-12), mgw-device-cloud-connector (SYNC-02).

## M1: Entscheidungen

**Persistenz.** Postgres und SQLite von Anfang an (OPS-02, FOG-03, OPS-03), beide über `database/sql` mit gemeinsamem SQL. Platzhalter werden als `?` geschrieben und für Postgres umgeschrieben. Zeiten als Unix-Millisekunden (`BIGINT`), JSON als `TEXT`, damit es keine Dialektunterschiede bei Typen gibt. Treiber: `modernc.org/sqlite` (reines Go, kein cgo, wichtig für arm64-Images), `pgx/v5/stdlib`. Abgelehnt: Mongo als Plattformvorgabe, weil die Anforderungen Postgres festlegen; zuerst nur SQLite, weil die Sperrsemantik für Timer bei mehreren Replicas (RUN-06, OPS-01) sonst erst spät geprüft wird.

**Nebenläufigkeit.** Jeder Zustandsübergang einer Instanz läuft in einer Transaktion, die die Instanzzeile sperrt (Postgres `FOR UPDATE`, SQLite `BEGIN IMMEDIATE`). Ein Timer oder Job wird in derselben Transaktion gelöscht, in der seine Wirkung festgeschrieben wird. Eine zweite Replica findet ihn danach nicht mehr. Das ergibt „genau einmal“ ohne Leader-Wahl.

**Aufgabentypen.** Ein Service Task erzeugt einen Job. Ausgeführt wird er außerhalb der Transaktion, abgeschlossen in einer neuen Transaktion, die prüft, ob der Job noch existiert und noch derselben Ausführung gehört (TASK-11). Timeout und Wiederholungen setzt die Engine (TASK-07). M1 liefert nur die Schnittstelle (TASK-00), noch keine Plattform-Typen. Ein Typ, der zwar erlaubt, aber in dieser Engine nicht vorhanden ist, wird beim Deploy abgelehnt.

**Modellformat.** Der Camunda-Dialekt bleibt die Grundlage, damit Designer und bpmn-js weiter passen. Abweichungen:
- Aufgabentyp über `senergy:taskType` statt `camunda:type="external"` mit `camunda:topic` (MIG-04 schreibt um).
- Bedingungen brauchen `language="javascript"`. Timer-Werte sind Text, mit `language="javascript"` ein Ausdruck.
- Mapping-Werte sind Text oder `camunda:script` mit JavaScript (EXPR-02). Text mit `${` wird abgelehnt (EXPR-01).

**Variablen-Scopes** nach Camunda (`AbstractVariableScope.setVariable`): Eingabe-Mappings erzeugen lokale Variablen am Element. `setVariable`, `resultVariable` und Ausgabe-Mappings schreiben lokal, wenn die Variable dort existiert, sonst auf Instanzebene.

**Gateways.** Die parallele Zusammenführung zählt Token wie Camunda 7 (Anzahl eingehender Flüsse), nicht je Fluss. Die inklusive Zusammenführung wartet, solange ein anderes aktives Token sie erreichen kann (Camunda 7). Beide sind gegen Camunda nachzutesten (BPMN-15); das ist in M1 nicht geschehen.

**IDs.** Instanz-IDs sind global eindeutig, das ist strenger als „je Mandant“ (RUN-01). Ein `PUT` auf eine fremde, nicht sichtbare ID gibt einen Konflikt ohne Details.

**Rechte in M1.** Nur Owner und Admin-Rolle. Die Prüfung liegt hinter einer Schnittstelle (`controller.Authorizer`), in die M2 permissions-v2 einsetzt. JWT nach service-commons, mit Signaturprüfung, wenn eine Keycloak-URL konfiguriert ist (SEC-04).

**API.** `net/http` mit dem Pattern-Routing der Standardbibliothek. Ressourcen unter `/namespaces/{ns}/...`, Korrelation unter `/messages`. Paging über `limit` und `offset`, die Antwort enthält immer `total`. OpenAPI über swag aus den Go-Typen des Servers (CLI-01).

**Grenzen (Vorgaben, konfigurierbar).** 100 Token je Instanz (LONG-16); 1 MiB je Variable, 4 MiB je Instanz (LONG-08); 10 000 Verlaufseinträge je Instanz als Ringpuffer (LONG-03); 2 s je Skriptauswertung (EXPR-03).

**Weitere Festlegungen im Modellformat** (betreffen den Designer im web-ui):
- `senergy:retries` und `senergy:timeout` (ISO-8601-Dauer) am Service Task (TASK-07).
- Message-Events mit `senergy:*`-Eventattributen oder ohne `messageRef` sind Geräte- bzw. Import-Events. Ihr Nachrichtenname ist `<deployment_id>_<element_id>`, stabil über Updates (DEP-04). Benannte Nachrichten (`messageRef` mit `name`) behalten ihren Namen.
- Bedingungen müssen ein Boolean liefern; sonst entsteht ein Incident.
- `NaN` und `Infinity` aus Skripten werden als String gespeichert, weil JSON sie nicht kennt; `Number(x)` liest sie wieder.

## M1: Stand

Geprüft mit `go test -race ./...` gegen SQLite und Postgres 16 (testcontainers). Die 63 exportierten Modelle werden alle gelesen; ihre Befunde sind ausschließlich solche, die MIG-04 umschreibt oder meldet (Test `TestExportedModelsParseWithOnlyMigrationIssues`, braucht `AE_BPMN_EXPORT_DIR`, die Modelle liegen bewusst nicht im Repo).

Teilweise umgesetzt:
- DEP-03: BPMN, Version und Start-Events atomar; Eventbeschreibungen, Incident-Einstellungen, Aufbewahrung und Zeitpläne folgen mit M2/M3.
- MOD-06: Deployments eines Modells über den Filter `model_id`; der Versionsabstand wird nicht berechnet.
- MOD-08: Modellverweis wird beim Löschen als gelöscht markiert; das Löschen von Nutzern folgt in M2.
- INC-01/02: Incidents entstehen in der Engine und beenden den Lauf (heutige Vorgabe „stoppen“). Benachrichtigung und Restart fehlen bis M2.
- EVT-08: keine Metrik für Korrelationen ohne Ziel.
- SEC: nur Owner und Admin-Rolle; Teilen über permissions-v2 in M2.
- CLI-02: deckt die bestehende API ab; Debugging, IO, Zeitpläne und Module kommen mit ihren Meilensteinen.

## M1: bekannte Lücken

- **Speicherlimit der Sandbox (EXPR-03):** goja kennt kein Speicherlimit. Wirksam sind das Zeitlimit und das Größenlimit für Ergebnisse. Ein Skript, das in 2 s viel Speicher belegt, wird nicht abgefangen. Ein hartes Limit braucht einen eigenen Prozess oder eine andere Laufzeit.
- **Camunda-Vergleich (BPMN-15, MIG-05)** steht aus.
- **Aufbewahrung nach Alter** fehlt bis M2. Bis dahin begrenzt nur der Ringpuffer den Verlauf.
- **CI** fehlt im Repo, also auch die Drift-Prüfung für die OpenAPI-Dateien in `docs/`.

## M2: Entscheidungen

**Default-Permissions (NS-05), entschieden 2026-10-12.** In permissions-v2 gelten die `DefaultPermissions` eines Topics bei jeder Prüfung für alle Ressourcen des Topics (`pkg/controller/check.go`, v1.0.0), es sind keine Startrechte. NS-05 setzt das anders voraus. Umgesetzt ist deshalb je Namespace und Ebene (`models`, `deployments`, `instances`):
- `shareable`: Rechte anderer aus permissions-v2 zählen. Sonst haben nur Owner und Admin-Rolle Zugriff, auch wenn in permissions-v2 etwas geteilt ist.
- `initial_rights`: Gruppen und Rollen, die jede neue Ressource beim Anlegen bekommt, wie `InitialGroupRights` in device-repository.
- `topic_default_permissions`: die echten topic-weiten Rechte, Vorgabe leer.
- `register`: Ressourcen nicht teilbarer Ebenen trotzdem in permissions-v2 eintragen, Vorgabe ja (entschieden). Wird eine Ebene eingetragen, die es vorher nicht war, trägt die Engine den Bestand nach.

Abgelehnt: `DefaultPermissions` wörtlich zu nehmen, weil dann eine Freigabe für alle Ressourcen eines Topics gilt und „nur für den Besitzer“ mit Startrechten nicht abbildbar ist.

**Owner (SEC-09, SEC-10).** Die Engine prüft den Owner selbst; er hat immer alle Rechte, unabhängig von permissions-v2. Ein direkter Entzug in permissions-v2 wirkt also nicht. Der Eintrag dort wird erst beim nächsten Eintragen wieder ergänzt (Owner-Wechsel, neue Registrierung). Eine laufende Reparatur über das Kafka-Signal von permissions-v2 folgt mit M3.

**Asynchrones Eintragen.** Neue Ressourcen tragen `perm_registered = 0`; `permissions.Service.Sync` schreibt sie nach permissions-v2 und setzt das Flag. Löschungen schreiben im selben Commit eine Zeile in `permission_removals`, die Sync abarbeitet. So läuft kein HTTP-Aufruf in einer offenen Transaktion, und ein Ausfall von permissions-v2 verzögert nur das Teilen. Abgelehnt: synchrones Schreiben vor dem Commit, weil dann jeder Start, auch jeder Timer-Start, an der Verfügbarkeit von permissions-v2 hängt.

**Neustart nach Incident (INC-02 bis 04).** Der Incident setzt nur `restart_at` mit Backoff (Vorgabe 1 min, verdoppelt bis 1 h, nach Anzahl aufeinanderfolgender Incident-Läufe). Gezählt und neu gestartet wird zum fälligen Zeitpunkt in einer eigenen Transaktion, die erst das Deployment und dann die Instanz sperrt, in derselben Reihenfolge wie ein Update. Zählen im Incident hätte die umgekehrte Reihenfolge und damit Deadlocks unter Postgres bedeutet. Über `max_restarts` (Vorgabe 10) je Fenster (24 h) wird das Deployment als fehlerhaft markiert und der Owner benachrichtigt; zurückgesetzt wird es durch ein Update oder `POST .../reset-restarts`. Ein Stopp bricht einen geplanten Neustart ab.

**Benachrichtigungen** gehen über eine Outbox (`notifications`) und werden nach dem Commit gesendet, mit Wiederholung und Verwerfen nach 24 h. Format wie process-incident-api heute: Titel „Process-Incident in <Deployment>“, Topic `incident`, `ignore_duplicates_within_seconds=3600`, Kopie an developer-notifications.

**Aufbewahrung (HIST-04).** Wirksam ist der kleinste Wert aus Deployment-Wert bzw. Vorgabe (Namespace, sonst global 7 Tage), Obergrenze des Namespace, globaler Obergrenze und Obergrenze des Instanz-Owners. Ein Deployment-Wert über den Grenzen des Owners wird abgelehnt. Das Aufräumen läuft in der Housekeeping-Runde (Vorgabe jede Minute) für 50 Deployments je Runde und danach für jeden Nutzer mit Grenze. Beendete Instanzen werden ganz gelöscht; bei allen anderen wird der Verlauf vor der Grenze entfernt (HIST-06). Instanzen mit geplantem Neustart bleiben.

**Namespaces (NS-02)** liegen in der Datenbank. Die Konfiguration legt beim Start nur fehlende an; danach gilt die Admin-API (`PUT`/`DELETE /namespaces/{ns}`). Topics werden vor dem Speichern geschrieben. Ein Namespace mit Ressourcen lässt sich nicht löschen. Jede Replica lädt die Namespaces alle 10 s neu.

**Nutzer löschen (SEC-08, SEC-13, SEC-14).** `GET /users/{id}/dependents` und `DELETE /users/{id}`, nur für Admins. Fremde Abhängige sind derzeit Instanzen anderer auf Deployments des Nutzers. Vorgabe ist Ablehnen mit 409 und Liste (entschieden); mit `user_deletion: cascade` am Namespace werden sie gelöscht und ihre Owner benachrichtigt. Die Rechte des Nutzers an fremden Ressourcen werden zuerst entfernt; scheitert permissions-v2, wird nichts gelöscht. Seine Ressourcen listet die Engine mit einem unsignierten Token des Nutzers, wie device-repository es tut, weil permissions-v2 Signaturen nicht prüft.

**Interne Aufrufe (SEC-03)** an permissions-v2 laufen mit Client-Credentials (`AUTH_ENDPOINT`, `AUTH_CLIENT_ID`, `AUTH_CLIENT_SECRET`). Der Client braucht die Realm-Rolle `admin`, weil er Topics anlegt und Startrechte setzt. Ohne `PERMISSIONS_V2_URL` gilt nur Owner und Admin-Rolle.

**Metriken** auf eigenem Port (seit M3 `INTERNAL_PORT`, vorher `METRICS_PORT`, Vorgabe 8081), damit Kong sie nicht routet: `automation_engine_runs_started_total`, `_runs_ended_total`, `_incidents_total`, `_automatic_restarts_total`, `_correlations_total{result}` (EVT-08), `_task_executions_total`, `_task_execution_seconds`, `_timer_delay_seconds`, `_storage_rows`, `_database_bytes`, `_stalled_instances` (LONG-13, `STALLED_AFTER`), `_instance_activities_top` (LONG-14).

## M2: Lücken

- Kafka-Fallback `user` (SEC-14) und das Rechte-Signal von permissions-v2 brauchen Kafka und folgen mit M3.
- Die Rechte aus `processmodel`, `smart_service_releases` und `smart_service_instances` gehen erst mit der Migration in die neuen Topics über (MIG-08, M7).
- Abhängige über Module (NS-08) gibt es erst mit M5; Zeitpläne und IO-Daten beim Löschen eines Nutzers mit M3.
- Laufen mehrere Replicas `Sync` gleichzeitig, kann in einem seltenen Ablauf (Löschen während einer laufenden Registrierung in einer anderen Replica) ein verwaister Eintrag in permissions-v2 bleiben. Er gewährt keinen Zugriff, weil die Ressource in der Engine fehlt.
- Listen auf teilbaren Ebenen holen alle freigegebenen IDs aus permissions-v2 und filtern mit `IN`. Bei sehr vielen Freigaben je Nutzer wird das teuer.
- Außerhalb dieses Repos: user-management muss Vorabprüfung und Löschen aufrufen; Kong und Ladon brauchen Admin-Regeln für `/users` und `PUT`/`DELETE /namespaces`.

## M3: Entscheidungen (2026-10-12)

**Stufen.** a) Gerätekommandos mit Geräteauswahl und Vorbereitungs-API, b) Process-IO, Benachrichtigung, HTTP-Aufruf, Zeitpläne, c) Event-Worker und Kafka-Signale.

**Wiederverwendung von external-task-worker v0.1.2 (entschieden).** Importiert werden die Vertragstypen (`lib/messages`: `Command`, `ProtocolMsg`, `TaskInfo`), der device-repository-Zugriff mit Token-Exchange und Cache (`lib/devicerepository`), der Marshaller-Client (`lib/marshaller`) und der Timescale-Client (`lib/timescale`). Die Orchestrierung (Protokollnachricht, Teilaufgaben für Gruppen, Eingaben auf Pfaden, Auswertung der Antwort) sitzt dort in Methoden des Camunda-Workers und wird an das Job-Modell der Engine portiert; die Herkunft steht in den Dateien. Sobald CLI-11 einen stabilen Ort schafft, wechseln die Importe dorthin.

**Modellformat Gerätekommando.** `senergy:taskType="device_command"`, `senergy:completionStrategy` (`optimistic` oder `pessimistic`, Vorgabe pessimistic). Die Task-Definition bleibt das `payload`-JSON im Modell wie heute; die Geräteauswahl steht typisiert je Element in der Deployment-Version (DEP-05) und wird bei der Ausführung eingesetzt.

**Event-Worker (entschieden).** Der event-worker wird Teil dieses Repos als eigene Rolle desselben Binaries, getrennt startbar und skalierbar, und arbeitet DB-gekoppelt:
- Eventbeschreibungen liegen in der Engine-Datenbank mit fortlaufender Revision; Löschungen hinterlassen einen Grabstein. Jeder Worker hält alle Beschreibungen im Speicher und holt nur Änderungen seit seiner letzten Revision. Das ersetzt `process-deployment-done` und invalidiert gezielt (DEP-08).
- Ein Worker abonniert nur Topics, für die es Beschreibungen gibt, statt aller Gerätedaten-Topics per Regex.
- Korrelation in-process gegen die Datenbank; bei QoS 1 wird der Offset erst nach dem Commit bestätigt; Idempotenzschlüssel `topic:partition:offset` (EVT-05).
- Auswertung (Unmarshalling, Characteristic, Skript) in einem gemeinsamen Paket, das auch die Rekonstruktion in M4 nutzt (EVT-10).
- Abgelehnt: Kafka-entkoppelt (kompaktiertes Topic plus HTTP-Korrelation) und API-entkoppelt, weil beide Tokens, ein Publisher und im Fog eine zweite Lösung brauchen. Preis der Entscheidung: jeder Worker-Pod ist Datenbank-Client.

## M3: Stand

- **Gerätekommando** (`pkg/tasks/devicecommand`): Protokollnachricht über Kafka oder HTTP, Teilaufgaben je Gerät bei Gerätegruppen (`GROUP_SCHEDULER` sequential oder parallel), optimistische und pessimistische Abschlussstrategie, Antworten und Fehler über die Topics `response`/`errors` oder `/responses` und `/errors` am internen Port, Zeitüberschreitung als Job-Timeout. Metriken mit den Namen des external-task-worker (`external_task_worker_task_command_send_count_vec`, `external_task_worker_task_marshalling_latency`), damit der cost-calculator sie weiter liest (TASK-12).
- **Geräteauswahl und Vorbereitung** (`pkg/prepare`, `POST /namespaces/{ns}/prepared-deployments`): Kriterien aus dem Modell, Optionen über device-selection, typisierte Auswahl je Element in der Deployment-Version; fehlende oder unzulässige Auswahl ist ein Validierungsfehler vor der Rechteprüfung.
- **Process-IO** (`/io/values`, Aufgabentyp `process_io`), **Benachrichtigung** (`notification`), **HTTP-Aufruf** (`http`, Richtlinie je Namespace und Betriebsart, geprüft beim Verbindungsaufbau).
- **Zeitpläne** (`/namespaces/{ns}/schedules`): Cron mit optionalen Sekunden und Deskriptoren, Feuern im Scheduler der Engine.
- **Eventbeschreibungen** entstehen beim Deployen und bei Änderungen an Gerätegruppen (Kafka `device-groups`) neu; `GET .../deployments/{id}/event-descriptions` zeigt sie.
- **Event-Worker** (`ROLE=event`, `pkg/events`, `pkg/server/events.go`): hält die Beschreibungen im Speicher, holt Änderungen per Revision jede Sekunde und lädt alle 10 Minuten vollständig neu; abonniert nur die Topics mit Beschreibungen in der Consumer-Gruppe `EVENT_CONSUMER_GROUP`; QoS 1 korreliert vor der Offset-Bestätigung, QoS 0 danach nebenläufig; Nachrichten älter als eine Stunde werden verworfen; Fehler gehen gedrosselt als Benachrichtigung an den Owner.
- **Kafka-Signale**: `user` (Löschung als Rückfall, SEC-14) und optional das Rechte-Signal von permissions-v2 (`PERMISSIONS_KAFKA_TOPIC`, SEC-10).
- Konfiguration: `Default()` ist durch einen Test gegen die eigene Validierung abgesichert. Der Rauchtest des Binaries lief für beide Rollen (Engine mit leerer Konfiguration; Event-Rolle bricht ohne `KAFKA_URL` mit einer klaren Meldung ab).

## M3: Lücken

- Bei `update_strategy=drain` gelten die Eventbeschreibungen der aktuellen Version. Instanzen einer alten Version, die auf ein in der neuen Version entferntes oder geändertes Geräteevent warten, bekommen es nicht mehr.
- Fog (MQTT statt Kafka für Kommandos und Gerätedaten) folgt mit M6; `MODE=fog` wählt bisher nur die HTTP-Richtlinie.
- Die Importe aus external-task-worker v0.1.2 und event-worker v1.2.0 bleiben, bis CLI-11 einen stabilen Ort schafft.
- `PERMISSIONS_KAFKA_TOPIC` ist ohne Wert abgeschaltet; ohne das Signal bleibt ein in permissions-v2 entzogenes Administrationsrecht des Owners dort entzogen. Zugriff in der Engine behält der Owner trotzdem, weil sie den Owner lokal prüft.
- Außerhalb dieses Repos: der cost-calculator muss die Engine-Pods statt der external-task-worker-Pods abfragen; die Repos event-worker, event-deployment und der Topic `process-deployment-done` werden mit der Umstellung stillgelegt; Kong darf den internen Port nicht routen.
