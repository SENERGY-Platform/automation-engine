# Plan: Automation-Engine

Grundlage: `docs/anforderungen.md` (Stand 2026-10-12). Die IDs in Klammern verweisen dorthin.

Die Anforderungen beschreiben ein Programm, nicht eine einzelne Änderung. Sie werden in Meilensteinen umgesetzt, die jeweils für sich lauffähig und getestet sind.

| # | Meilenstein | Kern-IDs | Stand |
|---|---|---|---|
| M1 | Ausführungskern: BPMN-Teilmenge, JS-Sandbox, Token-Ausführung, Timer, Korrelation, Läufe, Verlauf, Namespaces aus Konfiguration, neue REST-API, Go-Client mit Test-Client | BPMN-01–18, EXPR-01–05, DATA-01–03, NS-01–03/06/11 (Konfiguration), MOD-01/03–05, DEP-02/04, RUN-01–12 (ohne 07), HIST-01–03/05, LONG-01–05/08/12/16, EVT-01/02/05, CLI-01/03/04/08/09 | umgesetzt, siehe unten |
| M2 | permissions-v2 (Topics je Namespace, Startrechte, Owner-Schutz), Incident-Behandlung mit Restart und Backoff, Aufbewahrung mit Obergrenzen, Nutzer löschen, Admin-API für Namespaces, Prometheus-Metriken, Token-Provider im Client | NS-02/05/07, SEC-01–14, INC-02–04, HIST-04/06, LONG-03 (Alter), LONG-13/14, MOD-02/07/08, OPS-05, CLI-10 | umgesetzt, siehe unten |
| M3 | Aufgabentypen Gerätekommando, Process-IO, Benachrichtigung, HTTP-Aufruf; Zeitpläne; Event-Worker als Rolle des Binaries, Eventbeschreibungen, Gruppen-Expansion; Vorbereitungs-API; Kafka `user` und Rechte-Signal | TASK-01–12, IO-01–03, BPMN-09/10, RUN-07, DEP-01/05–10, EVT-01–08, NS-04/11 | umgesetzt, siehe unten |
| M4 | Debugging: Outbox, WebSocket-Live-Strom, historisches Debugging, Rekonstruktion von Event-Auswertungen | SYNC-03, DBG-*, EVT-09–13, CLI-05 | umgesetzt, siehe unten |
| M5 | Smart-Services-Module, Watcher, Wartungsprozeduren | TASK-20–29, SMS-* | umgesetzt, siehe unten |
| M6 | Fog-Betriebsart und Synchronisation | FOG-*, SYNC-*, LONG-06/07/09–11/14/15 | umgesetzt, Abnahme auf arm64-Gateway offen |
| M7 | `deprecated/`-Schicht, Migration aus Camunda, Modell-Umschreibung | API-02/03/08, MIG-* | offen |

Was andere Repos, den Betrieb oder die Plattformkonfiguration betrifft, steht gesammelt in `OFFENE-PUNKTE.md` und wird nach den Meilensteinen gemeinsam durchgegangen.

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

## M3: Entscheidungen (2026-10-12)

**Stufen.** a) Gerätekommandos mit Geräteauswahl und Vorbereitungs-API, b) Process-IO, Benachrichtigung, HTTP-Aufruf, Zeitpläne, c) Event-Worker und Kafka-Signale.

**Wiederverwendung von external-task-worker v0.1.2 (entschieden).** Importiert werden die Vertragstypen (`lib/messages`: `Command`, `ProtocolMsg`, `TaskInfo`), der device-repository-Zugriff mit Token-Exchange und Cache (`lib/devicerepository`), der Marshaller-Client (`lib/marshaller`) und der Timescale-Client (`lib/timescale`). Die Orchestrierung (Protokollnachricht, Teilaufgaben für Gruppen, Eingaben auf Pfaden, Auswertung der Antwort) sitzt dort in Methoden des Camunda-Workers und wird an das Job-Modell der Engine portiert; die Herkunft steht in den Dateien. Sobald CLI-11 einen stabilen Ort schafft, wechseln die Importe dorthin. Abgelöst am 2026-10-12: Die genutzten Teile sind ins Repo gezogen, siehe „Nach M5“.

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
- Fog (MQTT statt Kafka für Kommandos und Gerätedaten) folgt mit M6; `MODE=fog` wählt bisher nur die HTTP-Richtlinie. (Umgesetzt in M6, Stufe a.)
- Die Importe aus external-task-worker v0.1.2 und event-worker v1.2.0 sind seit 2026-10-12 durch Code im Repo ersetzt (siehe „Nach M5“).
- `PERMISSIONS_KAFKA_TOPIC` ist ohne Wert abgeschaltet; ohne das Signal bleibt ein in permissions-v2 entzogenes Administrationsrecht des Owners dort entzogen. Zugriff in der Engine behält der Owner trotzdem, weil sie den Owner lokal prüft.

## Nach M3 (2026-10-12)

- **Owner-Wechsel**: `HasAdministrate` fragt permissions-v2 mit `CheckPermission` und einem Token des neuen Owners (Keycloak-Token-Exchange) statt die Rechte der Ressource selbst auszuwerten. Damit zählen auch Rechte über Gruppen und Rollen.
- **Tracing** nach dem Muster von smart-service-repository (Commit 573979f): `pkg/tracing`, Beschreibung im README. Offen: Datenbankzugriffe sind nicht instrumentiert (für `database/sql` gäbe es otelsql, eine weitere Abhängigkeit); Kafka-Nachrichten tragen keinen Trace-Kontext, weil die Gegenseiten ihn weder schreiben noch lesen; der Span einer Aufgabe ist nicht mit der Anfrage verknüpft, die die Instanz gestartet hat (dafür müsste der Trace-Kontext mit der Instanz gespeichert werden).

## M4: Entscheidungen (2026-10-12)

**Umfang.** Fog-Teile (DBG-12, DBG-15, EVT-13) kommen mit M6, DBG-17 (K) entfällt vorerst. Stufen: a) Änderungsstrom, Debug-Zustand, historisches Debugging, b) Live-Endpunkt und Client, c) Rekonstruktion von Event-Auswertungen und aktueller Wert.

**Änderungsstrom (SYNC-03, entschieden: Outbox plus LISTEN/NOTIFY).** Jede Schreiboperation auf einer Instanz legt im selben Commit eine Zeile in `changes` an (Sequenz, Instanz, Aktivitätssequenz). Postgres meldet sie zusätzlich mit `pg_notify` beim Commit; jede Replica hält eine LISTEN-Verbindung. SQLite und Postgres pollen die Outbox als Rückfall, solange jemand zuschaut. Weckrufe sind nur Hinweise: Ein Zuschauer liest danach den Stand der Instanz selbst, verlorene oder doppelte Weckrufe schaden nicht. Die Synchronisation (M6) liest dieselbe Tabelle mit eigenen Positionen; bis dahin wird sie nach Alter aufgeräumt.

**Ein Zustandsmodell für live und historisch (DBG-02, DBG-13).** `model.DebugState` beschreibt Instanz, aktive Elemente mit Wartegrund, Variablen mit Typ, letzter Änderung und änderndem Element, offene Incidents und Elementzähler. Live wird er aus dem Laufzeitzustand gebaut, historisch durch Abspielen des Aktivitätsverlaufs.

**Vollstände (DBG-14).** In festen Abständen des Aktivitätsverlaufs (Obergrenze / 20) schreibt die Engine den Live-Zustand als Vollstand in `history_snapshots`, mit vollen Variablenwerten. Ein Schritt wird aus dem letzten Vollstand davor und den Aktivitäten dazwischen rekonstruiert. Vollstände unterhalb des ältesten verbliebenen Eintrags werden mit dem Ringpuffer verdrängt. Werte über `MaxActivityValueBytes` stehen im Verlauf gekürzt; zwischen zwei Vollständen zeigt die Rekonstruktion sie als gekürzt an.

**Rekonstruktion auf dem Server (entschieden).** `GET .../instances/{id}/history?seq=<seq>` (oder `time=`) liefert den Zustand in Snapshot-Form; Go-Client und web-ui navigieren nur.

**Live-Endpunkt (DBG-01, entschieden: coder/websocket v1.8.15, ISC).** Nach dem Snapshot berechnet der Server Deltas als Unterschied zum zuletzt gesendeten Zustand plus die neuen Aktivitäten; der Client übernimmt sie ohne eigene Ableitung. Zusammenfassen bei schnellen Schleifen ergibt sich daraus (DBG-10). Token per `Authorization`-Header (Go-Client, über Kong) oder als erste Nachricht (Browser); im zweiten Fall prüft die Engine die Signatur selbst, `KEYCLOAK_CERT_URL` ist dann Pflicht.

**Event-Rekonstruktion (EVT-10).** Werte über timescale-wrapper `/queries` mit dem Token des Owners (Token Exchange), Spalten aus den Ausgaben des Service wie im timescale-tableworker, Auswertung mit `events.Evaluator` wie im Event-Worker. Grundlage ist die Eventbeschreibung, die aus der Deployment-Version des Laufs neu erzeugt wird, weil gespeichert nur die der aktuellen Version vorliegt; Gerätegruppen werden dabei mit ihrer heutigen Zusammensetzung expandiert.

## M4: Stand

- **Änderungsstrom** (`changes`, SYNC-03): eine Zeile je Schreiboperation einer Instanz im selben Commit, Postgres meldet sie zusätzlich per `pg_notify`. `pkg/live.Hub` weckt die Live-Ströme einer Replica über LISTEN und pollt die Outbox als Rückfall (Postgres alle 2 s, SQLite alle 250 ms, nur solange jemand zuschaut). Aufgeräumt nach einer Stunde.
- **Debug-Zustand** (`pkg/debug`, `GET .../debug`, DBG-02): aktive Elemente, Wartegründe mit Gerätekommando-Teilaufgaben, Eventquellen, Timer und Joins, Variablen mit Typ, letzter Änderung und änderndem Element, offene Incidents, Zähler für Elemente und genommene Sequenzflüsse (DBG-06, DBG-07). Werte über 4 KiB werden gekürzt und einzeln geladen (DBG-04).
- **Historisches Debugging** (`GET .../history`, DBG-13, DBG-14): Vollstände alle `MaxActivities/20` Einträge (mindestens 50) und mindestens täglich, höchstens 30 je Instanz; Rekonstruktion aus dem letzten Vollstand und den Aktivitäten danach. Ein Test vergleicht an jedem Schritt Rekonstruktion und Live-Zustand, auch nach Verdrängung im Ringpuffer.
- **Live-Endpunkt** (`GET .../live`, DBG-01, DBG-03, DBG-08–10): Snapshot, Deltas als Unterschied zum zuletzt gesendeten Zustand plus neue Aktivitäten, Endzustand; Fortsetzen ab Sequenz; neue Snapshots bei großer Lücke oder langsamem Client; höchstens 5 Verbindungen je Nutzer und Replica (HTTP 429); Abbruch mit Code 4401 bei abgelaufenem Token.
- **Event-Rekonstruktion** (`GET .../event-evaluations`, EVT-10–12) und aktueller Wert wartender Events (`evaluate_events`, DBG-18, im Live-Strom alle 30 s): Werte über timescale-wrapper mit dem Token des Owners, Auswertung mit `events.Evaluator`. Abweichungen zwischen Rekonstruktion und Korrelation werden mit möglichen Ursachen genannt, nur wenn alle Werte auf eine Seite passen.
- **Client** (CLI-05): `Watch` in beiden Clients, `Follow` und `OpenHistory` darauf.
- Elementzähler enthalten jetzt auch Sequenzflüsse; `GET .../element-stats` liefert entsprechend mehr Einträge.

## M4: Lücken

- Fog-Teile (DBG-12, DBG-15, EVT-13) folgen mit M6 (umgesetzt in Stufe d); DBG-17 (K) ist nicht umgesetzt.
- Die Suche im Verlauf (DBG-16, S) beschränkt sich auf die vorhandenen Filter der Aktivitätsliste (Art, Element); eine Suche nach Variablenwerten fehlt.
- Werte über 4 KiB stehen im Aktivitätsverlauf gekürzt. Zwischen zwei Vollständen zeigt die Rekonstruktion sie gekürzt, `history/variables` meldet sie dann als nicht verfügbar.
- Die Zeit eines aktiven Elements stammt aus dem letzten Durchlauf des Elements; bei mehreren Token am selben Element ist sie die des jüngsten.
- Auf Postgres folgt die Sequenz des Änderungsstroms der Einfügereihenfolge, nicht der Commit-Reihenfolge. Für das Live-Debugging genügt das, weil Weckrufe nur Hinweise sind; die Synchronisation (M6) läuft auf SQLite mit einem Schreiber.
- Import-Events werden nicht rekonstruiert, solange offen ist, ob Importdaten in Timescale landen (EVT-12). Lange Listen mit festen Einträgen kommen aus Timescale als Map zurück; ein Unmarshalling, das eine Liste erwartet, scheitert dann und zeigt den Fehler an der Auswertung.
- Wie weit die Rekonstruktion zurückreicht, regelt die Aufbewahrung in Timescale (timescale-rule-manager); die Engine kennt sie nicht.

## M5: Entscheidungen (2026-10-12)

Grundlage sind die Analysen der acht Worker, von `smart-service-module-worker-lib` und von smart-service-repository (Stand `origin/main` 2026-10-06).

**Kompatibilität der Module (entschieden: voll).** Jede Zeichenkette in den Eingaben eines Modul-Tasks wird wie heute als Go-Template gegen die Variablen gerendert (`{{.param}}`, `brl`/`brr`), `prescript*`- und `postscript*`-Eingaben laufen mit derselben Skript-API (`variables`, `inputs`, `outputs`, `util`, `deviceRepo`, `console`). Bestehende Designs laufen ohne Umschreiben.

**Wiederverwendung statt Portierung.** Middleware, Skriptumgebung und Templates kommen aus `smart-service-module-worker-lib`, die Handler von `info`, `import`, `export`, `analytics` und `device_repository` aus den Worker-Repos (Pseudo-Versionen auf `origin/main` vom 2026-10-06). Sie nehmen nur kleine Schnittstellen (`GetInstanceUser`, `ListExistingModules`, `UseModuleDeleteInfo`), die die Engine gegen ihre eigene Datenbank erfüllt. Ihr Camunda-Teil bleibt ungenutzt. Neu geschrieben werden `process_deployment`, `process_deployment_start` und `watcher`, weil sie Dienste ansprechen, die die Engine ersetzt (process-deployment, process-engine-wrapper, watcher-db). Die Bibliotheken bleiben Abhängigkeit, bis CLI-11 einen Ort für sie schafft; der Preis ist eine Build-Kopplung an sechs Repos. Abgelöst am 2026-10-12: Der Code ist ins Repo gezogen, siehe „Nach M5“.

**Module (SMS-12).** Tabelle `modules` an der Instanz, Schlüssel in `module_keys`. Löschinfo entweder eine URL (DELETE mit Token des Owners, 2xx und 404 gelten als gelöscht, wie heute) oder eine Ressource der Engine (Deployment, Instanz, Beobachtung). Gelöscht wird über die Outbox `module_removals` mit Wiederholung, nicht im Request: es bleibt nichts verwaist (SMS-06). Instanzen mit Modulen fallen nicht unter die Aufbewahrung, weil ihre Ressourcen weiterlaufen.

**Module bei jedem neuen Lauf (entschieden, korrigiert).** Zuerst war eine Richtlinie je Namespace mit Abweichung je Deployment geplant (`delete` oder `keep_keyed`). Korrigiert: Jeder neue Lauf einer Instanz entfernt alle ihre Module, ob nach Parameteränderung (SMS-09), Update mit `restart` oder Neustart nach einem Incident. Grund: Nur kurzlebige Prozesse sollen Module anlegen. Dafür lehnt ein Namespace mit `forbid_cycles` Zyklen im Sequenzfluss beim Deploy ab, weil sich eine Schleife statisch nicht als endlich erweisen lässt; `smart-services` setzt das, `processes` nicht. Schlüssel bleiben für Wartungsprozeduren, die Module derselben Instanz aktualisieren.

**Wartungsprozeduren (SMS-13, entschieden: geteilte Variablen).** Message-Startereignisse mit eigenen Formfeldern. Ein Start legt eine Instanz mit `parent_id` an. Modul-Eingaben, Templates und Skriptvariablen lesen und schreiben die Variablen der Elterninstanz, Module hängen an ihr. Der Nachrichtenname ist der Name der Message, ohne Umbenennung (SMS-02).

**Gesundheitsprüfung der Module (entschieden: behalten).** Stündlich je Modultyp wie heute (Pipeline läuft, Import und Export existieren, Deployment existiert, gestartete Instanz läuft); ein Befund wird Modulfehler und erscheint im Instanzstatus, kein Incident.

**Status (SMS-11).** `status` an jeder Instanz: `running`, `ready` (Lauf abgeschlossen), `error` mit Meldung aus Laufende, Incident oder Modulfehler.

**Abschluss eines Deployments (SMS-04).** `process_deployment` deployt synchron im selben Schritt. Die Nachricht `deployment_done_<id>` wird für die ausführende Instanz vorgemerkt und beim nächsten Warten auf diesen Namen sofort zugestellt, statt sie 5 s später zu senden. Dafür bekommt ein Message-Event optional `senergy:messageName`, einen JavaScript-Ausdruck für den Namen; M7 schreibt `${done_event}` darauf um. `permission_done_<gid>` aus `device_repository` wird ebenso vorgemerkt; einen Sender gibt es heute nicht (SMS-05), wartende Designs liefen also nie weiter.

**Deploy-Parameter.** `process_deployment` setzt Eingaben, Eventvariablen und Timer einzelner Elemente (`{bpmnId}.parameter.*`, `.variables.*`, `.time`). Die Deployment-Version bekommt dafür `parameters` je Element, die die Eingabe-Mappings bzw. `senergy:variables` und Timerdefinitionen überschreiben; das web-ui kann sie im Deploy-Dialog ebenso setzen (DEP-01). Analytics-Events (`.event.flow_id`) sind tot und entfallen.

**Ohne Gateway.** `prefer_fog_deployment` deployt bis M6 in der Cloud; M6 ergänzt das Gateway.

**Stufen.** a) Module, Outbox, Status, Elterninstanzen, Nachrichtenpuffer, Zyklenprüfung, API; b) Modul-Laufzeit mit wiederverwendeten Handlern und Gesundheitsprüfung; c) `process_deployment`, `process_deployment_start`, `watcher`, Wartungsprozeduren; d) Startparameter mit Optionen (SMS-08), Parameteränderung (SMS-09), Variablen löschen (SMS-10).

## M5: Stand

- **Module** (SMS-12): Tabelle `modules` mit Schlüsseln, API zum Auflisten, Lesen und Löschen, Löschen der Ressourcen über die Outbox `module_removals` (URL mit Owner-Token oder Ressource der Engine im passenden Namespace). Instanzen mit Modulen fallen nicht unter die Aufbewahrung; beim Löschen eines Nutzers werden keine Entfernungen eingereiht.
- **Effekte von Aufgaben**: Module, Variablen der Wurzelinstanz und vorgemerkte Nachrichten entstehen in der Transaktion, die den Job abschließt. Kann die Engine ein Ergebnis nicht übernehmen (Instanz inzwischen gestoppt, Commit gescheitert), macht der Aufgabentyp die Ressourcen rückgängig; vorher bestehende Module bleiben dabei unberührt.
- **Neuer Lauf entfernt Module** und Wartungsprozeduren des vorigen (SMS-03, SMS-09); `forbid_cycles` lehnt Schleifen in `smart-services` ab.
- **Laufzeit der Module** (`pkg/modules`): Middleware, Skript-API und Templates der Worker-Bibliothek; `info`, `import`, `export`, `analytics`, `device_repository` mit dem Verhalten der Worker (seit 2026-10-12 im Repo, siehe unten); `process_deployment`, `process_deployment_start` und `watcher` neu gegen die Engine. Gesundheitsprüfung stündlich je Typ.
- **Status** (SMS-11) an jeder Instanz, Format der Modulfehler wie bisher.
- **Wartungsprozeduren** (SMS-13) mit geteilten Variablen und Modulen; Message-Starts in `smart-services` nur über die Instanz.
- **Startparameter** (SMS-08) mit Optionen aus device-selection, Werteformat der Releases, `auto_select_all`; `json_string_parameters` speichert Listen in String-Feldern als JSON wie Camunda.
- **Parameteränderung** (SMS-09), Variablen setzen und löschen in jedem Zustand (SMS-10).
- **Deploy-Parameter** je Element (Eingaben, Eventvariablen, Timer, Startvorgaben) für Module und Deploy-Dialog.
- **Nachrichtenpuffer** und `senergy:messageName` (SMS-04); `done_event` jeder Modulaufgabe wird vorgemerkt (SMS-05).

## M5: Lücken

- Gateway-Deployments (`prefer_fog_deployment`, Gateway-Instanzen von `process_deployment_start`) kommen mit M6; bis dahin Cloud.
- Die alte REST-API von smart-service-repository (Releases, Designs, `/instances-by-process-id`) folgt mit der `deprecated/`-Schicht in M7, ebenso die Migration bestehender Designs, Releases, Instanzen und Module.
- Module heißen `<instanz>.<job>` statt `<prozessinstanz>.<camunda-task>`; die Migration muss bestehende Module übernehmen, ohne ihre IDs zu ändern.
- Wie bisher sind `import` und `export` nicht idempotent: stürzt die Engine zwischen dem Anlegen beim Dienst und dem Commit ab, bleibt die Ressource dort ohne Modul.
- Die Fehler der Worker-Handler wurden zunächst mit übernommen; mit der Integration sind zwei behoben (siehe unten).
- Generische Beobachtungen (`watch_request`) laufen unter der HTTP-Richtlinie des Namespace statt über den isolierten Client des Watchers; in der Cloud ist sie standardmäßig aus.
- `process_deployment_start` vermerkt `state: completed` nicht mehr in den Moduldaten; der Status der gestarteten Instanz ist über die Engine abrufbar.
- Startwerte ab 3000 Zeichen wurden bisher durch Platzhalter ersetzt (Grenze von Camunda); die Engine speichert sie direkt bis zur Variablengrenze.

## Nach M5: Worker-Code integriert (2026-10-12)

**Entscheidung.** Die Repos der Module-Worker (`smart-service-module-worker-lib`, `-info`, `-import`, `-export`, `-analytics`, `-device-repository`) und der event-worker werden abgeschafft. Ihr Code steht nicht mehr in `go.mod`, er ist hier neu implementiert oder übernommen. Damit entfällt die Build-Kopplung an sechs Repos, die M5 für die Wiederverwendung in Kauf genommen hatte. Ebenso ersetzt sind die Pakete aus external-task-worker v0.1.2, die M3 importiert hatte; dieses Repo nutzen aber device-command und live-data-worker weiter.

**Wo.**
- `pkg/modules/compat`: Skript-API (`variables`, `inputs`, `outputs`, `util`, `deviceRepo`, `console`), Templates und Modellformate der Worker-Bibliothek.
- `pkg/modules/{info,imports,export,analytics,devicegroups}.go`: die Handler, als `Handler` über `Task` ohne Camunda-Typen. Undo ist generisch in `runtime.go`: bestehende Module bleiben, neue werden über ihre Löschinfo entfernt.
- `pkg/events/unmarshal.go`: Marshaller und gecachter Zugriff auf device-repository mit Client-Credentials und den Cache-Invalidierungssignalen von service-commons.
- `pkg/devices/repository.go`, `modifier.go`: Zugriff auf Geräte, Gerätetypen, Protokolle und Gerätegruppen mit dem Token des Owners und Cache (bisher `lib/devicerepository`), `SplitModifier` (bisher `lib/devicegroups`). Die Modelltypen kommen direkt aus `models/go`, `lib/devicerepository/model` waren nur Aliase darauf.
- `pkg/tasks/devicecommand/{messages,marshaller,timescale}.go`: Kommando-Payload und Protokollnachricht (bisher `lib/messages`, Felder und JSON-Namen maschinell gegen das Original abgeglichen), Client des marshaller-Dienstes, letzte Werte über timescale-wrapper.

**device-repository `/v3`.** Die Engine nutzt `device-repository/v3` v3.4.0 statt `/v2`; die Clients und Modelle sind für die genutzten Aufrufe gleich, die Golden-Tests laufen unverändert. `/v2` bleibt indirekt über `device-selection/v2/pkg/model`.

**Dokumentation der Skript-Umgebungen.** `pkg/scriptdoc` erzeugt mit `go generate ./pkg/scriptdoc` nach `docs/scripts`: für Pre- und Postscripts wie smart-service-module-worker-lib `script-env.d.ts`, `jsdoc.js` und `ace-code-completer.ts`, dazu TypeScript-Deklarationen der BPMN-Skripte und eine Markdown-Referenz beider Umgebungen. Methoden und Kommentare kommen aus dem Quelltext von `pkg/modules/compat`, Typen und Feldnamen per Reflection mit der Namensabbildung von goja, Abkündigungen aus den Kommentaren der Modelle. Tests prüfen, dass die Dateien aktuell sind, dass dokumentierte und aufrufbare Methoden und Globals übereinstimmen und dass jeder referenzierte Typ deklariert ist; `tsc --strict` übersetzt die Deklarationen. Gegenüber der Bibliothek unterscheidet sich die Ausgabe nur darin, dass mehrzeilige Kommentare in `jsdoc.js` auf jeder Zeile ein `*` tragen.

**Nachweis.** Die Testfälle der Worker liegen unter `pkg/modules/testdata` (analytics 13, device_repository 14, info 9) und laufen als Golden-Tests durch die Engine: dieselben Anfragen an flow-engine, device-manager und device-selection, dieselben Module und Variablen. Die Tests von Marshaller und Device-Repo des event-worker sind nach `pkg/events/unmarshal_test.go` übertragen. Für `export` gab es keine Tests; `export_test.go` prüft das Verhalten aus dem Quelltext. Aus external-task-worker sind die Aspekt-Tests von `lib/messages` übertragen; Repo-Zugriff, `SplitModifier` und die timescale-Abfrage haben eigene Tests. Die Kommandoausführung selbst war schon seit M3 portiert und getestet.

**Abweichungen vom Verhalten der Worker.**
- `info` überspringt ein Zusatzfeld, das kein String ist, und liest die übrigen weiter; der Worker brach bei ihm ab (`break`).
- `analytics` mit Schlüssel bricht mit dem Fehler ab, wenn die Modulsuche scheitert; der Worker ignorierte den Fehler und legte eine zweite Pipeline an. Die Meldung zu einer `pipeline_id`, die kein String ist, nennt jetzt die Pipeline statt der Gerätegruppe.
- `analytics` liest Gerätegruppe und Geräte für die Dienstkriterien nicht ein zweites Mal; das Ergebnis ist dasselbe.
- `persistData` bleibt nur als JSON-Text wirksam, ein Boolean gilt wie beim Worker als `false`.
- Gerätekommandos: Eine Antwort von device-repository außer 2xx ist ein Fehler mit Pfad und Status. Bisher wurde der Inhalt trotzdem gelesen, und es folgten Folgefehler wie ein leeres Gerät oder „service not found“.
- Gerätekommandos mit `prefer_event`: Liefert timescale-wrapper weniger Abfragen oder Spalten zurück als angefragt, ist das ein Fehler. Bisher lief der Zugriff in einen Panic.
- Event-Rolle: Ist device-repository nicht erreichbar (5xx oder Transportfehler), wird eine QoS-1-Nachricht erneut gelesen, und der Owner wird nicht benachrichtigt. Das entspricht dem event-worker. Seit M3 hatte die Engine stattdessen benachrichtigt und die Nachricht verworfen.

## M6: Entscheidungen (2026-10-12)

Grundlage sind die Analysen von process-sync (`master` und Branch `feat/platform-source-of-truth`), mgw-process-sync-client, mgw-external-task-worker, event-worker im Fog-Modus, mgw-device-cloud-connector, mgw-last-value, dem device-repository-Mirror und dem Prozessmodul `main/process` (v1.5.4) in mgw-module-repository.

**Protokoll (entschieden: nur das neue).** Die Cloud-Engine spricht nur das neue, versionierte Sync-Protokoll. Gateways mit Camunda bleiben bei process-sync, bis ihr Prozessmodul aktualisiert und migriert ist (M7); beide Protokolle laufen parallel, ohne sich zu stören. Verworfen: das alte Protokoll zusätzlich bedienen, weil die Engine dann Camunda-IDs, `known`-Listen und Warden-Logik erben würde.

**Transport.** Wie bisher über mgw-device-cloud-connector, der nur `processes/{nid}/cmd/#` (Cloud → Gateway, lokal `processes/cmd/...`) und `processes/state/#` (Gateway → Cloud, in der Cloud `processes/{nid}/state/...`) überbrückt. Die neuen Nachrichten liegen darunter in `engine/v1/...`, getrennt von den Topics von process-sync. Der Connector stellt höchstens einmal zu (Clean Session, QoS 0 nach oben, Puffer im Speicher); Zuverlässigkeit entsteht deshalb im Protokoll: Sequenzen, Bestätigungen und erneutes Senden nach Frist (SYNC-02, SYNC-08).

**Cloud-Seite bei mehreren Replicas (entschieden: geteilte Subscriptions, SYNC-10).** Alle Replicas abonnieren `$share/<gruppe>/processes/+/state/engine/v1/#`. Eine Nachricht eines Gateways wird in einer Transaktion übernommen, die das Netz sperrt, und nur lückenlos ab der zuletzt bestätigten Sequenz; ältere werden erneut bestätigt, Lücken verworfen, das Gateway sendet ab der Bestätigung neu. Bestätigt wird erst nach dem Commit. Reihenfolge und Doppelzustellung über mehrere Replicas sind damit im Protokoll gelöst, nicht im Broker. Verworfen: eine eigene Rolle mit Leader-Wahl, weil sie nur eine aktive Replica erlaubt.

**API auf dem Gateway (entschieden: lesen lokal, schreiben per Sync).** Lokal gibt es nur lesende Routen, Health und Metriken, ohne Tokenprüfung wie bei den übrigen Gateway-Diensten. Deployments, Starts, Stopps und Löschungen kommen nur über die Synchronisation aus der Cloud. Ein Gerät im LAN kann so keine Prozesse mit HTTP-Aufrufen ins lokale Netz deployen. Verworfen: volle API ohne Prüfung; keine lokale API.

**Fog-Laufzeit (FOG-01).** Dasselbe Binary mit `MODE=fog`:
- Gerätekommandos auf `command/{lokale Geräte-ID}/{lokale Service-ID}` des Gateway-Brokers mit `{"command_id","data"}`; Antworten auf `response/#`, Fehler auf `error/command/#`, zugeordnet über ein eigenes Präfix der `command_id`. Die Teilaufgabe steht in der Datenbank, die Protokollnachricht wird für die Antwort neu gebildet; ein Neustart verliert also keine Zuordnung (mgw-external-task-worker hielt sie nur im Speicher und hörte `error/command` nicht ab).
- Geräteevents von `event/#` mit lokalen IDs. Eventbeschreibungen tragen die lokalen IDs von Gerät und Service, die der Describer aus device-repository liest; das Gateway braucht dafür keine eigenen Zuordnungstabellen (process-sync schickte sie getrennt mit, mit einem Fehler beim Service-Mapping).
- device-repository-Mirror statt device-repository, Marshaller im Prozess (Bibliothek marshaller v2 und v1 mit Konzept-Repository aus dem Mirror, wie mgw-external-task-worker), mgw-last-value statt timescale-wrapper (lokale IDs, fehlender Wert ist ein Fehler).
- Benachrichtigungen über mgw-cloud-proxy, dasselbe Format.
- Process-IO übernimmt die Engine selbst; process-io-worker und seine Postgres entfallen.

**Speicherbudget (LONG-07).** `STORAGE_BUDGET` begrenzt die Datenbank. Ab 80 % kürzt die Engine Verlauf über die Aufbewahrung hinaus, älteste Einträge zuerst, bis 70 %, und meldet das einmal je Tag an den Nutzer des Netzes; Laufzeitzustand wird nie gekürzt. Schlägt ein Schreiben wegen eines vollen Datenträgers fehl, rollt SQLite die Transaktion zurück; die Engine hält die Ausführung an, meldet sich als nicht gesund und läuft weiter, sobald Schreiben wieder gelingt.

**Stufen.** a) Fog-Laufzeit; b) Sync-Protokoll und Gateway-Seite; c) Cloud-Seite mit Netzen, Reconciler, Netzübersicht, Gateway-Zielen für `process_deployment`; d) Debugging von Fog-Instanzen; e) Images für arm64, Dauertest-Harness, Modfile als offener Punkt. Jede Stufe wird nach grünen Tests committet und gepusht (entschieden).

## M6: Stand

- **a) Fog-Laufzeit** (2026-10-12): `MODE=fog` mit Gateway-Broker über `pkg/mqtt` (paho.mqtt.golang unter EDL-1.0, entschieden), Gerätekommandos über MQTT mit Zuordnung aus der Datenbank (`pkg/tasks/devicecommand/local.go`), Marshaller im Prozess mit Konzept-Repository aus dem Mirror (`localmarshaller.go`), mgw-last-value, Geräteevents mit lokalen Topics im selben Prozess (`pkg/events/local.go`, Dekodierung wie platform-connector), lesende API ohne Token, Speicherbudget mit Kürzen des Verlaufs und Pause bei vollem Datenträger. Nebenbei behoben: `debug.Apply` ordnete neue Elementstatistiken anders als die Datenbank, der Test schlug deshalb sporadisch fehl.
- **b) Sync-Protokoll und Gateway-Seite** (2026-10-12): `pkg/fogsync/protocol` (Version 1, Topics unter `engine/v1/`, getrennt von process-sync) und `pkg/fogsync/gateway`. Nach unten Soll-Zustand je Ressource mit Version (Namespace, Deployment mit Version und Eventbeschreibungen der Cloud, Instanz starten, stoppen, löschen), idempotent und je Ressource bestätigt, Fehler mit Meldung (SYNC-01, SYNC-13). Nach oben Pakete des Änderungsstroms nach der bestätigten Position, je Instanz auf den letzten Stand gefaltet, eins zur Zeit, ohne Bestätigung nach 30 s erneut mit Backoff bis 5 min; ist die Fortsetzung geleert, geht ein Snapshot (SYNC-02, LONG-09, LONG-10). Status beim Start und alle 5 min mit Versionen, Positionen, angewendeten Ressourcen und Speicher (SYNC-11, SYNC-12, LONG-14). Nachrichten einer anderen Protokollversion wendet das Gateway nicht an.
- **c) Cloud-Seite** (2026-10-12): `pkg/fogsync/cloud` in den Engine-Replicas mit geteilter Subscription; Pakete des Änderungsstroms werden unter Sperre des Netzes nur lückenlos übernommen und nach dem Commit bestätigt; Soll-Zustand je Netz und Ressource in `network_desired` mit gesendeter und angewendeter Version, erneutes Senden nach `SYNC_RESEND_AFTER`, nach einem Status sofort; Gateways außerhalb des Protokollfensters werden markiert und nicht bedient; Netze ohne Kontakt seit `NETWORK_CLEANUP_MAX_AGE` werden mit ihren Ressourcen entfernt. Deployments und Instanzen tragen `network_id`; ein Deployment für ein Gateway wird in der Cloud gespeichert, aber nicht aktiviert, seine Eventbeschreibungen lesen die lokalen Topics und bleiben dem Event-Worker der Cloud fern; Starts erscheinen als `pending`, bis das Gateway sie meldet. Rechte über permissions-v2 `hubs` (SEC-05). `GET /networks`, `GET /networks/{id}`, Filter `network_id` an Deployments und Instanzen, Metriken `network_last_contact_seconds` und `network_backlog`. `process_deployment` mit `prefer_fog_deployment` wählt wie bisher das erste Gateway, das alle ausgewählten Geräte trägt.
- **d) Debugging von Fog-Instanzen** (2026-10-12): Lesende Debug-Routen einer Gateway-Instanz (Debug-Zustand, historischer Zustand, Durchläufe, Aktivitäten, Elementzähler, Variablen) fragen über das Sync-Protokoll beim Gateway an (`request`/`response/{replica}`, Antwort an die anfragende Replica, Frist `SYNC_CALL_TIMEOUT`); ist das Gateway nicht erreichbar, liefert der Debug-Zustand den zuletzt gemeldeten Stand mit `offline: true` (DBG-12, DBG-15). Der Live-Endpunkt abonniert die Änderungen der Instanz auf dem Gateway mit einer Lease von 1 min, die die Cloud erneuert, solange jemand zuschaut. Die Event-Rekonstruktion liest Durchläufe und Aktivitäten vom Gateway und die Gerätewerte wie in der Cloud aus Timescale; Daten aus Zeiten ohne Verbindung fehlen dort, die Engine nennt das als Einschränkung (EVT-13).
- **e) Images und Dauertest** (2026-10-13): Das Dockerfile kompiliert für die Zielplattform, Images für amd64 und arm64 ohne arm/v7 (FOG-02); Workflows `tests.yml` (Format, Vet, Abgleich der erzeugten Dateien in `docs/`, Build, Tests) und `dev.yml`. Harness für den Dauertest in `pkg/soak` mit CSV-Bericht und Prüfung, dass Ressourcen nach dem Einschwingen nicht weiter wachsen (LONG-15, LONG-06, LONG-07); die Abnahme auf einem arm64-Gateway steht aus. `mqtt.Memory` kann Nachrichten verwerfen statt sie aufzuzeichnen, sonst wüchse der Speicher im Dauertest mit.
  Der erste Lauf über 30 min (Budget 4 MiB, zwei Verbindungsabbrüche) fand Fehler, die jetzt behoben sind: Das Budget kürzte auf dem Gateway Instanzen, deren Ende die Cloud noch nicht kannte, sie blieben dort `pending`; ein Snapshot hätte in der Cloud den Verlauf aller auf dem Gateway gekürzten Instanzen gelöscht; `permission_removals` wuchs ohne permissions-v2 unbegrenzt; Soll-Zustand in der Cloud, angewendete Ressourcen auf dem Gateway und damit jede Statusmeldung wuchsen mit jedem Start. Jetzt behalten Aufbewahrung und Budget eine Instanz, bis die Cloud ihr Ende bestätigt hat; eine beendete Instanz, die einem Snapshot fehlt, bleibt in der Cloud; ohne permissions-v2 wird nichts zum Entfernen vorgemerkt; der Start einer beendeten Instanz wird in der Cloud sofort vergessen, auf dem Gateway, sobald die Instanz dort entfernt ist. Damit eine spätere Ressource mit derselben ID trotzdem neuer ist als das zuvor Angewendete, beginnen Versionen des Soll-Zustands bei der Zeit in Millisekunden statt bei 1. Das behebt nebenbei, dass ein gelöschtes und mit derselben ID neu angelegtes Deployment auf dem Gateway übersprungen worden wäre. Die Laufzeittabellen erreichen ihren Ruhezustand erst nach den Aufbewahrungen von Änderungsstrom (1 h) und Empfangsbelegen (24 h); ein Budget darunter kürzt den ganzen Verlauf. Nach den Korrekturen lief derselbe Test 30 min mit 6 MiB Budget grün: 3599 Instanzen gestartet und abgeschlossen, keine bleibt `pending`, Datei zuletzt 4,7 MB, Heap und Goroutinen ohne Anstieg. Die Prüfung auf das Budget nimmt den Median des belegten Platzes im letzten Viertel, nicht die Spitze: Das Budget wird je Aufräumrunde durchgesetzt, und nach einer Offline-Phase bleiben Instanzen absichtlich, bis die Cloud ihr Ende kennt.

## M7: Entscheidungen (2026-10-13)

Grundlage sind die Routen und Datenmodelle von camunda-engine-wrapper, process-deployment, process-model-repository, process-io-api, process-incident-api, process-scheduler, smart-service-repository, process-sync (`master`) und process-fog-deployment, ihre Aufrufer im Workspace und im web-ui sowie 63 exportierte Modelle (57 verschiedene).

**Umfang der `deprecated/`-Schicht (entschieden: genutzte Routen).** Nachgebildet werden die Routen mit bekannten Aufrufern: web-ui, canary und snowflake-canary, kopierte Start-URLs (v2 und v1 `/deployment/{id}/start`) und api-aggregator, mit den Filtern, die sie senden. Camunda-Filter werden nur übersetzt, soweit genutzt (`maxResults`, `firstResult`, `sortBy`, `sortOrder`, `processInstanceBusinessKey`, `processDefinitionId`, `processDefinitionNameLike`, `finished`, `unfinished`, `with_total`). Die internen Routen der Worker (`/instances-by-process-id/...`, `variables-map`, Modul-Upserts, `/v2/event-trigger`, Admin-Routen von incident-api und process-io) entfallen, weil Worker, Event-Worker, Incidents und IO in der Engine sind. Verworfen: alle registrierten Routen, weil es für den Rest keine Aufrufer gibt.

**Übersetzung.** Die Schicht ruft nur den Controller der neuen API, mit dem Token des Aufrufers, und hält keinen Zustand (API-03). Alte Kennungen werden auf neue abgebildet, ohne Tabelle: Deployment-ID = vid bzw. process-deployment-ID (die Migration übernimmt sie unverändert), Prozessdefinitions-ID = `deplid_<id>:<version>:<id>`, Business Key = Instanz-ID. Ein Start mit `business_key` wird ein `PUT` mit dieser ID, also idempotent statt einer zweiten Instanz mit demselben Schlüssel. Abweichende Statuscodes der alten Dienste (401 statt 403, 500 bei unbekannter ID) werden nicht nachgebildet. Jede Route wird gezählt (`automation_engine_deprecated_requests_total{route}`).

**Quelle der Migration (entschieden: direkt).** Das Werkzeug liest die Mongo-Datenbanken der alten Dienste, die vid-Zuordnung und die Shards aus den Postgres-Datenbanken des Wrappers und die Deployments, Definitionen und laufenden Instanzen über die REST-API jedes Camunda-Shards und der Smart-Service-Camunda. Neue Abhängigkeit: `go.mongodb.org/mongo-driver` (Apache-2.0). Verworfen: Lesen über die alten APIs, weil process-deployment keinen Admin-Zugriff kennt und Deployments, die nur in Camunda existieren, dort fehlen.

**Form.** Unterbefehl `migrate` des Binaries. Ohne `-apply` Trockenlauf; es legt nur an, was fehlt, und ist damit wiederholbar; Bericht auf stdout, Exit-Code 0 (vollständig), 1 (teilweise), 2 (nicht ausführbar), wie `moses/tools/migratelegacy`.

**Startwerte beim Neustart (entschieden).** Camunda speichert auf History-Level `activity` keine ursprünglichen Startwerte. Je Formfeld des Start-Events wird der aktuelle Wert der gleichnamigen Laufzeitvariable genommen; Smart-Service-Instanzen nehmen ihre gespeicherten Parameter, Gateway-Instanzen die `start_parameters` aus process-sync. Der Bericht nennt, wo ein Prozess die Variable verändert haben kann.

**Gateways (entschieden: die Cloud übernimmt).** Die Migration liest `deployment_metadata` und Wardens aus process-sync und legt Deployments und Starts als Soll-Zustand des Netzes an. Hat ein Gateway das neue Prozessmodul, wendet es sie über die Synchronisation an. Bis dahin bedient process-sync die alten Gateways; `deprecated/process-sync` bildet nur Gateways mit der Engine ab. Lokale IO-Werte liest die Gateway-Engine beim ersten Start aus der alten process-io-Datenbank, wenn diese eingebunden ist.

**Modelle umschreiben (MIG-04).** Eine Bibliothek, die die Migration und ein Import-Endpunkt nutzen. Abgedeckt ist, was die Exporte enthalten: JUEL in Bedingungen, Mappings, Timern und Nachrichtennamen; Groovy mit `println`; Topics `pessimistic`, `optimistic`, `process_io` und die Modul-Topics; `http-connector` (Benachrichtigung oder HTTP-Aufruf); `{{DefinitionId}}` in IO-Schlüsseln. Gemeldet statt umgeschrieben: `mail-send`, `R/P1D` als Dauer, Cron mit 5 Feldern, Bedingungen ohne Sprache wie `was_active || true`. Der Vergleich mit Camunda (MIG-05) läuft gegen Camunda 7 im Container und lässt sich wie die Postgres-Tests abschalten.

**Stufen.** a) Umschreiben der Modelle mit Bericht und Camunda-Vergleich; b) `deprecated/`-Schicht; c) Migrationswerkzeug; d) Gateway-Übernahme und IO auf dem Gateway. Jede Stufe wird nach grünen Tests committet und gepusht.

## M7: Stand

- **a) Modelle umschreiben** (2026-10-13): `pkg/migrate/rewrite` arbeitet auf dem XML und lässt Layout und Diagramm unverändert; `POST /namespaces/{ns}/convert` bietet es ohne Speichern an. Alle 63 exportierten Modelle werden bis auf `mail-send` (10 Tasks) und zwei Bedingungen ohne Sprache vollständig umgeschrieben und bestehen danach die Prüfung beim Deploy. Abweichend von der Liste in MIG-04 schreibt die Umschreibung zwei Fälle um statt sie nur zu melden, beide mit Hinweis zur Prüfung: `R/P1D` als Dauer wird `P1D`, weil Camunda dabei ebenfalls nur einmal wartete; ein Cron mit 5 Feldern bekommt Sekunden, weil Camunda ihn abgelehnt hatte und das Modell nie lief. JavaScript-Bedingungen mit `return` auf oberster Ebene werden in eine Funktion gelegt. Der Vergleich mit Camunda 7.24 (MIG-05, `TestRewrittenExpressionsBehaveLikeCamunda`) wertet Bedingungen und Mappings aus den Exporten mit Zahlen, Strings, Booleans und `null` in beiden Engines aus. Gleich sind sie bis auf zwei dokumentierte Fälle: Ein String `"false"` ist in JUEL bei `== false` ein Boolean, und ganzzahlige Doubles schreibt Java als `30.0`. Listen und JSON-Objekte als Variablen sind nicht verglichen, weil ihre Darstellung in Camunda vom Weg abhängt, auf dem sie gesetzt wurden.
- **b1) `deprecated/` für Engine-Wrapper, Repository, IO, Incidents, Scheduler** (2026-10-13): `pkg/deprecated` unter `/deprecated/`, nur in der Cloud, mit Zähler `automation_engine_deprecated_requests_total{route}` und `Deprecation`-Header; in der OpenAPI als veraltet markiert (API-07). Die neue API bekam dafür zwei Felder, die das web-ui heute nutzt und die in der Schicht keinen Speicher haben dürfen: `created_by` an Zeitplänen (Widget-Bezug) und den Filter `not_owner` an Modellen. Ein Start mit `business_key` ist ein `PUT` mit dieser ID; abweichende Variablen ergeben 409 statt einer zweiten Instanz.
- **b2) `deprecated/` für process-deployment v3** (2026-10-13): Hin über `Convert` und die Geräteauswahl je Element; geänderte Eingaben, Timer, Benachrichtigungstexte und Startvorgaben werden Parameter der Deployment-Version, aber nur, wenn sie sich vom Modell unterscheiden und keinen Ausdruck enthalten, sonst würde ein Override die Ausdrücke des umgeschriebenen Modells ersetzen. Zurück wird die Elementliste aus dem deployten Prozess gebaut (Kriterien über `prepare.CriteriaOf`, Optionen nur mit `with_options`). Ein Update stoppt laufende Instanzen, wie es process-deployment über das Neu-Deployen in Camunda tat.
- **b3) `deprecated/` für smart-service-repository** (2026-10-13): Designs sind Modelle im Format des alten Designers; ein Release schreibt das Design um und deployt es mit Bezug auf das Modell, wofür die neue API jetzt `model_id` zusammen mit `xml` annimmt (Inhalt des Modells in anderer Form, MIG-04). Die Releases eines Designs sind die Versionen eines Deployments (SMS-03); die Release-ID ist die Deployment-ID, ältere Versionen `<id>:v<n>`, `new_release_id` folgt aus der Versionsdifferenz, `PUT .../parameters?release_id` zieht die Instanz um (SMS-09). Instanzen haben dafür jetzt `name` und `description` (`PUT .../instances/{id}/info`); bei Gateway-Instanzen gehören sie der Cloud und werden von der Replikation nicht überschrieben. Verworfen: je Release ein eigenes Deployment, weil die Engine eine Instanz nicht auf ein anderes Deployment umziehen kann und SMS-03 das Release als Update beschreibt.
- **b4) `deprecated/` für process-sync und process-fog-deployment** (2026-10-13): nur Gateways mit der Engine (entschieden). Eine Deployment-ID gilt zugleich als Camunda-ID der alten Routen; `is_placeholder` folgt aus dem Soll-Zustand des Netzes, `sync_date` aus dem letzten Kontakt. Kong leitet diese Pfade erst um, wenn kein Gateway mehr Camunda nutzt.
