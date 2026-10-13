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
- `info`, `import`, `export`, `analytics`, `device_repository`: das Verhalten der bisherigen Worker, im Repo implementiert und gegen deren Testfälle geprüft (`pkg/modules/testdata`). Eingaben mit Präfix `<typ>.`, Go-Templates gegen die Instanzvariablen (`{{.param}}`), `prescript*`/`postscript*` mit `variables`, `inputs`, `outputs`, `util`, `deviceRepo`. Die Skript-API beider JavaScript-Umgebungen (Modul-Skripte und BPMN-Skripte) steht in [docs/scripts](docs/scripts/README.md), als TypeScript-Deklarationen, JSDoc und Ace-Vervollständigung; erzeugt mit `go generate ./pkg/scriptdoc`.
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
- `ROLE=event`: Event-Worker. Liest die Eventbeschreibungen aus der Datenbank, abonniert die zugehörigen Gerätedaten-Topics in der Consumer-Gruppe `EVENT_CONSUMER_GROUP` und korreliert Treffer direkt. Braucht `KAFKA_URL` und für die Auswertung `DEVICE_REPOSITORY_URL` mit den `AUTH_*`-Werten; das Unmarshalling läuft im Prozess. Ist device-repository nicht erreichbar, wird eine QoS-1-Nachricht erneut gelesen statt dem Owner gemeldet.

## Gateway (Fog)

Mit `MODE=fog` läuft dasselbe Binary auf einem Gateway, als einziger Container des Prozessmoduls und mit SQLite:
- Gerätekommandos gehen an `command/{lokale Geräte-ID}/{lokale Service-ID}` des Gateway-Brokers (`MQTT_BROKER`, z. B. `tcp://broker:1881`); Antworten kommen über `response/#`, Fehler über `error/command/#`, erkannt am Präfix `LOCAL_COMMAND_PREFIX` (Vorgabe `automation-engine-`).
- Geräteevents kommen von `event/#`; die Event-Rolle läuft im selben Prozess.
- `DEVICE_REPOSITORY_URL` ist der device-repository-Mirror des Gateways, ohne Token; marshallt wird im Prozess.
- `LAST_VALUE_URL` ist mgw-last-value, für Services, die nur Events senden.
- `NOTIFIER_URL` zeigt auf den cloud-proxy (`http://{gateway}/notifications-v2/notifications`).
- `STORAGE_BUDGET` begrenzt die Datenbank in Bytes (Vorgabe im Fog 1 GiB, negativ heißt unbegrenzt). Ab 80 % wird der Verlauf über die Aufbewahrung hinaus gekürzt, älteste Einträge zuerst, bis 70 %, mit einer Warnung je Tag. Ist der Datenträger voll, hält die Ausführung an und `/health` meldet einen Fehler, bis Schreiben wieder gelingt.
- Die API ist lokal nur lesend und braucht kein Token; Deployments, Starts und Stopps kommen über die Synchronisation aus der Cloud.

Images gibt es für `linux/amd64` und `linux/arm64`, nicht für `arm/v7`. Das Dockerfile kompiliert auf der Plattform des Build-Hosts für die Zielplattform, arm64 braucht deshalb keine Emulation.

## Synchronisation mit Gateways

In der Cloud verbindet `SYNC_MQTT_BROKER` die Engine mit dem Broker, den die Gateways über mgw-device-cloud-connector erreichen. Deployments für ein Gateway entstehen über dieselbe API mit `network_id` (die Hub-ID) und laufen dort, nicht in der Cloud; Instanzen eines Gateways startet, stoppt und löscht die Cloud, ihren Zustand meldet das Gateway. Die Replicas teilen die Nachrichten der Gateways (`SYNC_SHARE_GROUP`); das Protokoll bestätigt und sendet selbst erneut (`SYNC_RESEND_AFTER`), Gateways ohne Kontakt seit `SYNC_OFFLINE_AFTER` gelten als offline, nach `NETWORK_CLEANUP_MAX_AGE` werden ihre Daten entfernt. `GET /networks` zeigt Kontakt, Versionen und Rückstand je Gateway, `GET /networks/{id}` Soll- und Ist-Zustand je Ressource. Rechte auf Gateways prüft permissions-v2 (Topic `hubs`: `rx` zum Lesen und Starten, `a` zum Deployen). Das Protokoll ist in `pkg/fogsync/protocol` beschrieben.

Debug-Routen und der Live-Endpunkt einer Gateway-Instanz fragen das Gateway über dasselbe Protokoll an und warten höchstens `SYNC_CALL_TIMEOUT` (Vorgabe 10 s). Ist es nicht erreichbar, antwortet der Debug-Zustand mit dem zuletzt gemeldeten Stand und `offline: true`, die übrigen Routen mit 503.

Namespaces aus `NAMESPACES` werden nur angelegt, wenn es sie noch nicht gibt; danach verwaltet `PUT /namespaces/{ns}` sie (Admins).

## API

OpenAPI in `docs/swagger.json` und `docs/swagger.yaml`, erzeugt mit `go generate ./...` aus den Go-Typen in `pkg/model` und den Annotationen in `pkg/api`. Nach Änderungen an API oder Typen neu erzeugen und mit einchecken.

Go-Client in `pkg/client`: `client.New(url)` für einen laufenden Server, `client.NewTestClient(ctx)` für Tests ohne Netz und Container; beide erfüllen dasselbe Interface.

## Modelle aus Camunda

`POST /namespaces/{ns}/convert` schreibt ein Modell für Camunda 7 in das Format der Engine um, ohne etwas zu speichern (MIG-04). JUEL wird zu JavaScript (ein einzelner Ausdruck behält seinen Typ, Text mit Ausdrücken wird ein Template-Literal, in dem `null` leer ist wie in JUEL), Groovy mit `println` zu `log`, die Topics `pessimistic`, `optimistic`, `process_io` und die der Module zu Aufgabentypen, der `http-connector` zum Aufgabentyp `notification` (Ziel `notification` des alten process-deployment) bzw. `http`, Nachrichtennamen wie `${done_event}` zu `senergy:messageName`. Die Antwort nennt, was umgeschrieben wurde, was von Hand zu prüfen ist und was nicht unterstützt wird (`mail-send`, Bedingungen ohne Sprache), dazu die Befunde unter den Regeln des Namespace. Bekannte Abweichungen zu Camunda: Ein String `"false"` ist in JUEL bei `== false` ein Boolean, in JavaScript nicht; ganzzahlige Doubles erscheinen in Texten als `30` statt `30.0`.


## Migration aus Camunda

`automation-engine migrate` übernimmt die Daten der alten Dienste (MIG-01 bis MIG-09). Die Engine wird wie zum Betrieb konfiguriert (`-config`, Umgebung); sie braucht `permissions_v2_url` und `auth_endpoint`, weil das Werkzeug als Admin schreibt und Rechte überträgt. Die Quellen:

- `MIGRATE_MONGO_URL`: die MongoDB der alten Dienste; Datenbanken nach den Namen der Plattform, änderbar mit `-db-*`.
- `-camunda`: Basis-URLs der Camunda-Engines (alle Shards), jeweils bis `/engine-rest`; `-camunda-user` und `MIGRATE_CAMUNDA_PASSWORD` für die Basic-Auth.

Ohne `-apply` ist es ein Trockenlauf. Ein Lauf legt nur an, was fehlt, behält IDs und leitet nötige neue IDs aus den alten ab; er lässt sich also wiederholen. Der Bericht nennt je Schritt, was übernommen, geplant, schon vorhanden, übersprungen, zu entscheiden oder fehlgeschlagen ist; Exit-Code 0 heißt vollständig, 1 offen oder teilweise, 2 nicht gelaufen. Zu entscheiden sind Ressourcen ohne gültigen Owner (`-owner alt=neu`), Modelle mit nicht unterstützten Teilen (z. B. `mail-send`) und Deployments, die nur in Camunda existieren (`-camunda-only <ids>`). `-no-restart` listet laufende Instanzen nur auf, statt sie neu zu starten; mit `-json` kommt der Bericht als JSON.

Modelle bleiben im Format des alten Designers, Deployments werden umgeschrieben und behalten den Bezug zum Modell, wenn Owner und BPMN übereinstimmen. Laufende Instanzen starten neu, mit dem Business Key als ID, wo er eindeutig ist, und den aktuellen Werten der Variablen, die wie die Startfelder heißen. IO-Werte folgen ihrer Instanz in Schlüssel und Bindung; Werte beendeter Instanzen entfallen.

Smart-Service-Designs werden Modelle im Namespace `smart-services`, die Releases eines Designs die Versionen eines Deployments mit der ID des neuesten Releases. Instanzen, deren Einrichtung beendet ist, werden mit ihren Modulen und Variablen übernommen und nicht erneut ausgeführt, weil das jedes Modul ein zweites Mal anlegen würde; eine Instanz in der Einrichtung startet neu. Löschverweise von Modulen auf process-deployment und process-sync werden zu Deployments und Instanzen der Engine, andere URLs bleiben. Was process-sync für Gateways hält, wird Deployment und Start des Netzes und läuft, sobald das Gateway die Engine hat; Owner ist der eine Administrator des Hubs oder der mit `-owner network:<id>=<user>` genannte.

## Tests

```sh
go test ./...
```

Store- und Engine-Tests laufen gegen SQLite und gegen Postgres in einem Container (testcontainers). Ohne Container-Runtime werden die Postgres-Varianten übersprungen; `AE_TEST_POSTGRES=off` schaltet sie ab. Der Vergleich der Umschreibung mit Camunda 7.24 (MIG-05) und die Leser der Migration starten Camunda und MongoDB ebenfalls im Container; `AE_TEST_CAMUNDA=off` und `AE_TEST_MONGO=off` schalten sie ab. `AE_BPMN_EXPORT_DIR=<Verzeichnis>` prüft Parser und Umschreibung zusätzlich gegen exportierte Modelle. Der Vergleich der Umschreibung mit Camunda 7.24 (MIG-05) und die Leser der Migration starten Camunda und MongoDB ebenfalls im Container; `AE_TEST_CAMUNDA=off` und `AE_TEST_MONGO=off` schalten sie ab.

### Dauertest (LONG-15)

`pkg/soak` lässt eine Gateway-Engine mit einer Cloud-Engine im selben Prozess unter gleichmäßiger Last laufen: Die Cloud startet Instanzen eines Prozesses, der auf ein Geräteevent wartet, ein Gerät abfragt, eine Sekunde wartet und eine Variable von etwa 1 KiB schreibt; simulierte Geräte antworten über den Broker des Gateways und senden Events. Die Verbindung zur Cloud fällt zeitweise aus. In festen Abständen landen Größe der Datenbank, belegter Platz, Heap, Goroutinen, offene Dateien, Instanzen und Rückstand der Synchronisation in einer CSV.

Ohne `SOAK_DURATION` läuft er 5 s lang in der normalen Suite und prüft nur, dass alle gestarteten Instanzen abschließen. Ab 30 min prüft er zusätzlich, dass die Ressourcen nach dem Einschwingen nicht weiter wachsen: Datenbankdatei, Heap, Goroutinen und offene Dateien im letzten Viertel gegen das zweite. Der belegte Platz muss im Median des letzten Viertels unter dem Budget liegen; Spitzen darüber sind erlaubt, weil das Budget nur je Aufräumrunde durchgesetzt wird und Instanzen nach einer Offline-Phase bleiben, bis die Cloud ihr Ende kennt.

Auf einem arm64-Gateway als vorab übersetztes Testbinary:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -o soak.test ./pkg/soak
# auf dem Gateway
SOAK_DURATION=72h SOAK_DIR=/data/soak ./soak.test -test.run TestSoak -test.timeout 0 -test.v
```

Variablen: `SOAK_DURATION`, `SOAK_DIR` (Datenbanken und Bericht, Vorgabe ein temporäres Verzeichnis), `SOAK_REPORT` (CSV, Vorgabe `SOAK_DIR/soak.csv`), `SOAK_SAMPLE_INTERVAL`, `SOAK_STARTS_PER_SECOND` (Vorgabe 2), `SOAK_EVENT_INTERVAL` (Vorgabe 500 ms), `SOAK_STORAGE_BUDGET` (Vorgabe 32 MiB; Änderungsstrom und Empfangsbelege werden erst nach 1 h und 24 h aufgeräumt, ein zu kleines Budget lässt keinen Verlauf übrig), `SOAK_OFFLINE_EVERY` und `SOAK_OFFLINE_FOR` (Vorgabe ein Viertel und ein Vierzigstel der Dauer). `SOAK_MQTT_BROKER` mit `SOAK_MQTT_USER` und `SOAK_MQTT_PASSWORD` nutzt einen echten Broker statt eines im Prozess; die Synchronisation läuft dann über dessen Topics `processes/cmd/...` und `processes/state/...`, deshalb auf einem Gateway ohne laufenden mgw-device-cloud-connector, sonst erreichen die Nachrichten die echte Cloud. Marshaller und Geräte-Repository sind im Test nachgebildet; gemessen wird das Verhalten der Engine über die Zeit, nicht die Umrechnung von Werten.
