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
