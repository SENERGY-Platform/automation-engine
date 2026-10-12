# Automation-Engine

Ersatz für Camunda 7 auf der SENERGY-Plattform: Modelle, Deployments und Instanzen von Prozessen und Smart Services in einer Engine. Anforderungen in `docs/anforderungen.md`, Umsetzungsstand und Entscheidungen in `PLAN.md`.

## Starten

```sh
go run . -config config.json
```

Die Konfiguration kommt aus `config.json` und aus Umgebungsvariablen (Namen in `pkg/configuration/config.go`, z. B. `DATABASE=postgres`, `POSTGRES_DSN`, `SERVER_PORT`, `NAMESPACES` als JSON). Vorgabe ist SQLite in `automation-engine.db`. Das Schema migriert die Engine beim Start selbst.

Für den Plattformbetrieb kommen dazu:
- `PERMISSIONS_V2_URL` mit `AUTH_ENDPOINT`, `AUTH_CLIENT_ID`, `AUTH_CLIENT_SECRET`: Teilen über permissions-v2; der Keycloak-Client braucht die Realm-Rolle `admin`. Ohne URL haben nur Owner und Admins Zugriff.
- `NOTIFIER_URL`, `DEVELOPER_NOTIFICATIONS_URL`: Benachrichtigungen bei Incidents.
- `INTERNAL_PORT` (Vorgabe 8081, ersetzt `METRICS_PORT`): Prometheus-Metriken unter `/metrics` und Antworten von Geräten über HTTP unter `/responses` und `/errors`. Nicht über Kong routen.
- `RETENTION_DEFAULT_DAYS`, `RETENTION_MAX_DAYS`: Aufbewahrung des Verlaufs.
- `KAFKA_URL`: Gerätekommandos und ihre Antworten (`RESPONSE_TOPIC`, `ERROR_TOPIC`), Änderungen an Geräten und Gerätegruppen (`DEVICE_TOPIC`, `DEVICE_GROUP_TOPIC`), Nutzerlöschungen (`USER_TOPIC`) und optional das Rechte-Signal von permissions-v2 (`PERMISSIONS_KAFKA_TOPIC`).
- `DEVICE_REPOSITORY_URL`, `MARSHALLER_URL`, `TIMESCALE_WRAPPER_URL`, `DEVICE_SELECTION_URL`, `IMPORT_DEPLOY_URL`, `IMPORT_REPOSITORY_URL`: Dienste für Gerätekommandos, Geräteauswahl und Geräteevents. Gerätekommandos brauchen zusätzlich `AUTH_ENDPOINT` für den Token-Exchange.
- `MODE` (`cloud` oder `fog`, Vorgabe `cloud`): wählt die Richtlinie für HTTP-Aufrufe aus Prozessen.
- `MAX_IO_VALUES_PER_USER`, `MAX_IO_VALUE_BYTES`: Grenzen für Process-IO.
- `MAX_LIVE_CONNECTIONS_PER_USER` (Vorgabe 5): Live-Debug-Verbindungen je Nutzer und Replica.
- `OTEL_ENDPOINT`: OTLP-Collector (gRPC) für Traces. Leer heißt Vorgabe von `gin-middleware/otelx` (`jaeger.logging.svc.cluster.local:4317`), `off` schaltet Tracing ab, etwa im Fog oder lokal.

## Smart Services und Module

Smart Services sind der Namespace `smart-services`. Ihre Aufgabentypen ersetzen die acht Module-Worker:
- `info`, `import`, `export`, `analytics`, `device_repository`: die Handler der bisherigen Worker, unverändert eingebunden. Eingaben mit Präfix `<typ>.`, Go-Templates gegen die Instanzvariablen (`{{.param}}`), `prescript*`/`postscript*` mit `variables`, `inputs`, `outputs`, `util`, `deviceRepo`.
- `process_deployment`, `process_deployment_start`: deployen bzw. starten Prozesse im referenzierten Namespace (`processes`) mit dem Token des Owners. Das Deployment verweist auf die verwaltende Instanz (`managed_by`). Den Abschluss meldet die Engine mit der Nachricht `deployment_done_<id>`, die die Instanz beim nächsten Warten darauf sofort erhält; Wartepunkte nennen ihren Namen mit `senergy:messageName` (JavaScript).
- `watcher`: prüft Geräteauswahlen (oder mit `ALLOW_GENERIC_WATCH_REQUESTS` beliebige Anfragen unter der HTTP-Richtlinie des Namespace) in Abständen und startet bei einer Änderung eine Wartungsprozedur.

Konfiguration: `DEVICE_MANAGER_URL`, `DEVICE_SELECTION_URL`, `FLOW_ENGINE_URL`, `FLOW_PARSER_URL`, `IMPORT_DEPLOY_URL`, `SERVING_SERVICE_URL`, `DEVICE_REPOSITORY_URL`, dazu die Optionen der Worker (`ANALYTICS_*`, `DEVICE_GROUP_*`, `INFO_ADDITIONAL_MODULE_DATA_FIELDS`, `WATCHER_MIN_INTERVAL`, `WATCHER_DEFAULT_INTERVAL`, `MODULE_HEALTH_CHECK_INTERVAL`). Ein Aufgabentyp ist nur registriert, wenn seine Dienste konfiguriert sind; sonst lehnt der Deploy ihn ab.

API:
- Module: `GET .../modules?instance_id=&module_type=&key=`, `GET|DELETE .../modules/{id}`. Gelöscht werden Ressourcen über eine Outbox mit Wiederholung; Instanzen mit Modulen fallen nicht unter die Aufbewahrung.
- Status jeder Instanz (`status`, `status_message`): `running`, `ready`, `error` aus Laufzustand und Modulfehlern der stündlichen Gesundheitsprüfung.
- Startparameter mit Optionen: `GET .../deployments/{id}/parameters`; `auto_select_all`-Felder füllt die Engine beim Start.
- Parameter ändern: `PUT .../instances/{id}/parameters`. Jeder neue Lauf entfernt die Module des vorigen; `smart-services` lehnt deshalb Schleifen im Sequenzfluss ab (`forbid_cycles`).
- Wartungsprozeduren: `GET .../instances/{id}/maintenance-procedures[/{procedure}/parameters]`, `POST .../{procedure}/start`. Message-Starts in `smart-services` starten nur so, nicht über `/messages`.
- Variablen setzen und löschen auch bei beendeten Instanzen: `PUT|DELETE .../instances/{id}/variables/{name}`.
- Deploy-Parameter je Element: `parameters` am Deployment überschreibt Eingaben, Eventvariablen, Timer und Startvorgaben.

## Debugging

- `GET .../instances/{id}/debug`: aktueller Zustand mit aktiven Elementen, Wartegründen, Variablen, offenen Incidents und Durchlaufzählern von Elementen und Sequenzflüssen. Große Werte kommen gekürzt, `GET .../instances/{id}/variables/{name}` liefert sie einzeln.
- `GET .../instances/{id}/live`: WebSocket mit Snapshot, Deltas und Endzustand; `after=<seq>` setzt nach einem Abbruch fort. Das Token kommt im `Authorization`-Header oder, für Browser, als erste Nachricht `{"type":"auth","token":"..."}`. Die zweite Variante gibt es nur mit `KEYCLOAK_CERT_URL`, weil das Token dann nicht über Kong läuft und die Engine die Signatur selbst prüft. Kong muss das Upgrade durchreichen.
- `GET .../instances/{id}/history?seq=|time=`: Zustand nach einem Schritt des Verlaufs, rekonstruiert aus Vollständen und Aktivitäten; `first_seq` nennt den frühesten noch rekonstruierbaren Schritt.
- `GET .../instances/{id}/event-evaluations?element_id=`: Auswertungen eines Geräteevents, rekonstruiert aus den Werten in Timescale, neben den tatsächlichen Korrelationen. Braucht `TIMESCALE_WRAPPER_URL`, `DEVICE_REPOSITORY_URL` und `AUTH_ENDPOINT` (Token des Owners); dieselben Werte erlauben `evaluate_events=true` am Debug-Zustand und am Live-Strom.
- Im Go-Client: `client.Follow` führt Snapshot und Deltas zusammen und setzt nach Abbrüchen fort, `client.OpenHistory` geht Schritt für Schritt durch den Verlauf.

Mehrere Replicas brauchen keine Sticky Sessions: Jede Schreiboperation einer Instanz landet im Änderungsstrom (Tabelle `changes`), Postgres meldet ihn per LISTEN/NOTIFY, SQLite wird gepollt.

## Tracing

Wie in den anderen Plattformdiensten über `gin-middleware/otelx`:
- Eingehende Anfragen übernehmen Trace-Kontext und Baggage des Aufrufers; otelx legt `user_id` und `username` aus dem Token in die Baggage.
- Anfragen an Plattformdienste (permissions-v2, Notifier, import-deploy, HTTP-Antwortziele von Gerätekommandos) tragen Trace-Kontext und Baggage weiter. Nicht weitergegeben wird an device-selection, device-repository und Marshaller, deren Clients keinen Kontext annehmen.
- Der Aufgabentyp `http` gibt nichts weiter: seine Ziele liegen außerhalb der Plattform, und die Baggage enthält Nutzer-IDs.
- Jede Ausführung einer Aufgabe und jede Auswertung eines Geräteevents ist ein eigener Span; Instanz, Deployment, Namespace und Owner stehen als `automation_instance_id`, `automation_deployment_id`, `automation_namespace` und `user_id` in Span und Baggage. Die Ausführung läuft im Hintergrund, ihr Span beginnt deshalb einen eigenen Trace statt an der startenden Anfrage zu hängen.
- Logeinträge mit Kontext (`InfoContext`, ...) tragen die Baggage und werden Ereignisse des Spans.

## Rollen

Dasselbe Binary läuft in zwei Rollen, getrennt skalierbar und an dieselbe Datenbank gebunden:
- `ROLE=engine` (Vorgabe): API, Ausführung, Aufgaben, Zeitpläne.
- `ROLE=event`: Event-Worker. Liest die Eventbeschreibungen aus der Datenbank, abonniert die zugehörigen Gerätedaten-Topics in der Consumer-Gruppe `EVENT_CONSUMER_GROUP` und korreliert Treffer direkt. Braucht `KAFKA_URL` und für die Auswertung `DEVICE_REPOSITORY_URL` mit den `AUTH_*`-Werten; das Unmarshalling läuft im Prozess.

Namespaces aus `NAMESPACES` werden nur angelegt, wenn es sie noch nicht gibt; danach verwaltet `PUT /namespaces/{ns}` sie (Admins).

## API

OpenAPI in `docs/swagger.json` und `docs/swagger.yaml`, erzeugt mit `go generate ./...` aus den Go-Typen in `pkg/model` und den Annotationen in `pkg/api`. Nach Änderungen an API oder Typen neu erzeugen und mit einchecken.

Go-Client in `pkg/client`: `client.New(url)` für einen laufenden Server, `client.NewTestClient(ctx)` für Tests ohne Netz und Container; beide erfüllen dasselbe Interface.

## Tests

```sh
go test ./...
```

Store- und Engine-Tests laufen gegen SQLite und gegen Postgres in einem Container (testcontainers). Ohne Container-Runtime werden die Postgres-Varianten übersprungen; `AE_TEST_POSTGRES=off` schaltet sie ab. `AE_BPMN_EXPORT_DIR=<Verzeichnis>` prüft den Parser zusätzlich gegen exportierte Modelle.
