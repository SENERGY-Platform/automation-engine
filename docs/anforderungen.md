# Anforderungen: Automation-Engine als Ersatz für Camunda 7

Stand 2026-10-12, mit den Entscheidungen aus Abschnitt 2.

Die neue Komponente heißt **Automation-Engine** (entschieden 2026-10-12). Im Text steht oft kurz „Engine“; gemeint ist dann immer die Automation-Engine. Camunda wird ausdrücklich als Camunda bezeichnet.

Quellen:
- Code der lokalen Checkouts unter `~/workspace/go`, `~/workspace/ts/web-ui` und `~/workspace/docker/process-engine`;
- Deployment-Definitionen in `rancher-2-defs`;
- Notizen in `senergy-wissen`;
- 63 aus dem process-model-repository exportierte Modelle (55 verschiedene Namen), die möglicherweise veraltet sind.

Was hier steht, ist die heutige Nutzung, nicht der Funktionsumfang von Camunda.

Prioritäten: **M** = muss, **S** = soll, **K** = kann. Jede Anforderung nennt kurz, woraus sie folgt.

---

## 0. Ausgangslage in Kürze

- **Zwei Camunda-Instanzen**:
  - die Haupt-Engine (`process-engine`, Camunda 7.24, ein Pod, Postgres 11.2);
  - eine zweite für Smart Services (`smart-service-engine`, Camunda **7.11**, eigene DB, ohne Wrapper, Mandanten und Aufräumen).
- **Dazu je Gateway eine Camunda 7.24** mit eigener Postgres im Prozess-Modul (`mgw-module-repository/main/process/Modfile.yaml`, ARM-Image nur für arm/v7 mit Java 8 und `-Xmx768m`).
- **Camunda wird als reine REST-Blackbox betrieben.** Cockpit und Tasklist sind entfernt. Genutzt werden:
  - Deployment und Start über `submit-form`;
  - Instanzen und Historie auf Instanzebene;
  - Message-Korrelation und External Tasks;
  - Incidents (lesend), Timer und Script-Auswertung.
- **Um die Engine herum** liegen Wrapper, Task-Worker, process-io, Incidents, event-deployment, event-worker, process-deployment, process-model-repository, process-scheduler, process-history-cleanup, process-sync, process-fog-deployment und acht Smart-Service-Module-Worker.
- **Ein großer Teil ihres Codes gleicht Lücken von Camunda aus**: Camunda vergibt eigene IDs, liefert keinen Änderungsstrom und kennt das Mandantenmodell der Plattform nicht. Beispiele:
  - die vid-Tabelle im Wrapper;
  - das Sharding, obwohl in Prod nur ein Shard existiert;
  - Postgres-Trigger auf Camunda-Tabellen im Sync-Client;
  - Schema-Migrationen in zwei Fremdservices;
  - Incident-Polling alle 5 s;
  - der Neustart von Instanzen auf zwei Ebenen im Fog.

## 1. Konsolidierungsumfang

| Komponente | Entscheidung | Begründung |
|---|---|---|
| Camunda (`process-engine`) | ersetzen | Ziel |
| camunda-engine-wrapper | **integrieren** | API-Fassade, Rechteprüfung und Lösch-Kaskaden werden Kernfunktionen der Engine; das vid-Mapping entfällt (DEP-02) |
| external-task-worker (optimistic, pessimistic) | **integrieren** | Gerätekommandos als nativer Aufgabentyp |
| process-io-worker, process-io-api | **integrieren** | Variablenspeicher am Lebenszyklus von Instanz und Deployment |
| process-incident-api | **integrieren** | Incident-Behandlung (Stopp, Restart, Notify) ist Laufzeitverhalten |
| process-model-repository | **integrieren** | Modell, Deployment und Update gehören zusammen (Abschnitt 5) |
| process-deployment, process-fog-deployment | **integrieren** | Einzige Dienste, die BPMN lesen und schreiben; gleicher Parser und Stringifier |
| event-deployment | **integrieren** | Eventbeschreibungen sind ein Artefakt des Deployments |
| event-worker | **eigene Rolle** (Variante A, entschieden) | Skalierung über die Gerätedaten-Topics bleibt getrennt; keine Meldungen für das Debugging (Abschnitt 10) |
| process-scheduler | **integrieren** | beliebig viele Zeitpläne je Deployment (1:n); heute In-Memory-Cron, 1 Replica, Zeitpläne überleben das Löschen des Deployments |
| smart-service-engine | **ersetzen durch dieselbe Engine** | Funktionsbedarf ist eine Teilmenge |
| smart-service-module-worker-* (8 Worker) | **integrieren** (entschieden) | als interne Aufgabentypen; keine externe Task-API für Dritte (Abschnitt 8) |
| mgw-external-task-worker | **integrieren** (Fog-Build) | Wrapper um dieselbe Worker-Lib |
| process-sync, mgw-process-sync-client | **integrieren** (entschieden) | Aus Zustandsspiegelung wird Replikation zwischen zwei gleichen Engines; auf dem Gateway im Engine-Prozess, in der Cloud in der Codebasis der Engine (Abschnitt 17) |
| process-history-cleanup (CronJob) | **entfällt** | Aufbewahrung wird Engine-Funktion |
| Sharding-DB, wrapper-db (viddb) | **entfällt** | Ein Shard in Prod; vid-Mapping entfällt mit eigener ID |
| smart-service-repository | **integrieren** (entschieden) | Designs, Releases, Instanzen, Module und Wartungsprozeduren hängen am Lebenszyklus der Smart-Service-Prozesse (Abschnitt 18) |
| api-aggregator | **abschalten** statt portieren | Seit 2026-10-01 abgelöst, aber noch deployt und in Kong geroutet |

## 2. Entscheidungen

| Frage | Entscheidung | Wirkt auf |
|---|---|---|
| Name der Komponente | Automation-Engine | gesamtes Dokument |
| Event-Verarbeitung integrieren? | Variante A: event-worker bleibt eigene Rolle | Abschnitt 10 |
| JUEL unterstützen oder ersetzen? | JUEL ersetzen, nur JavaScript | EXPR-01, MIG-04 |
| Laufende Instanzen bei der Umstellung | neu starten | MIG-02 |
| Lastanforderungen | keine besondere Berücksichtigung | OPS-01 |
| Zielhardware Fog | keine besondere Berücksichtigung; arm64 oder amd64, kein arm/v7 | FOG-02, FOG-04 |
| Persistenz Cloud | Postgres | OPS-02 |
| Update eines Deployments | Nutzer entscheidet beim Update; gilt auch für Smart Services und die von ihnen über das Prozess-Modul verwalteten Prozesse | DEP-04, SMS-03 |
| Externe Task-API | keine Nutzerintegration; Smart-Service-Module-Worker werden integriert | Abschnitt 8 |
| Aufbewahrung Verlauf und Incidents | Wert am Deployment, begrenzt durch Obergrenzen auf Konfigurations- (global, Namespace) und Nutzerebene; wirksam ist der kleinste dieser Werte (geändert 2026-10-12 nach Stakeholder-Feedback, vorher „der längste Wert gilt“); dieselben Daten dienen dem historischen Debugging | HIST-04, HIST-05 |
| Debugging | Markierung im Client; Schritt-für-Schritt auch auf historischen Daten; so detailliert wie möglich | Abschnitt 13 |
| Event-Debugging | nicht über Meldungen des event-worker, sondern implizit aus den Timescale-Daten; gilt auch für Fog-Prozesse | EVT-09 bis EVT-12, DBG-18 |
| Speichermessung auf Gateways | nicht nötig, wenn das Problem behoben ist | Abschnitt 12 |
| process-model-repository | integrieren | Abschnitt 5 |
| smart-service-repository | integrieren | Abschnitt 18 |
| Topic `export` im Prozess-Designer | veraltet, entfällt | BPMN-08 |
| HTTP-Aufruf aus Prozessen in der Cloud | konfigurierbar | BPMN-10 |
| Gateways auf arm/v7 | alte Versionen sind irrelevant | FOG-02, MIG-06 |
| Obergrenzen für Ringpuffer und Speicherbudget | werden bei der Implementierung festgelegt | LONG-03, LONG-07 |
| Business Keys | entfallen; Instanzen erhalten stabile, vom Aufrufer vorgebbare IDs, wie Deployments ohne vid | RUN-01, RUN-11, RUN-12 |
| Prozessdefinition | entfällt; es gibt Modelle, Deployments und Instanzen | DATA-01 bis DATA-04 |
| Alte APIs und Aliase | nur unter einem Pfad `deprecated/`; die neue API ist frei von Altlasten | Abschnitt 15 |
| Prozesse und Smart Services | im Hintergrund dasselbe; Abgrenzung über Namespaces, frei konfigurierbar und erweiterbar; je Namespace erlaubte oder verbotene Module bzw. Worker | NS-01 bis NS-10, Abschnitt 18 |
| Client-Bibliotheken | nur für Go; für JS/TS keine, die Frontends setzen die API selbst um, das web-ui wird angepasst | CLI-01 bis CLI-11 |
| Rechte | eigene Topics in permissions-v2 je Namespace und Ebene (Modell, Deployment, Instanz), heute also 6; Default-Permissions je Namespace konfiguriert | NS-05, MOD-02, MIG-08 |
| Owner | jede Ressource hat einen Owner in der Engine, wie Geräte und Hubs; der Owner hat zwingend Admin-Rechte in permissions-v2 | SEC-09 bis SEC-12 |
| Löschen eines Nutzers mit fremden Abhängigen | Löschkaskade und Fehlermeldung sind beide möglich; eine Konfiguration entscheidet | SEC-13, SEC-14 |
| process-sync und mgw-process-sync-client | in die Engine integrieren | SYNC-09 bis SYNC-13 |
| Erlaubte BPMN-Elemente | am Namespace konfigurierbar (Erlaubnis bzw. Verbot) | NS-06 |
| Zeitpunkt der Geräteauswahl | beides möglich (beim Deploy, über Startparameter); am Namespace konfigurierbar, ob angeboten | NS-11 |
| Verweise zwischen Namespaces | ja, ausdrücklich erlaubt | NS-08 |
| Ein Designer für alle Namespaces | ja | NS-09 |
| Alte Routen, insbesondere kopierte Start-URLs | Kong leitet die alten Pfade auf `deprecated/` um | API-08 |

---

## 3. Prozessmodell und BPMN-Umfang

### Was die exportierten Modelle enthalten

Ausgewertet wurden 63 Dateien (55 verschiedene Modelle). Die Zählung erfolgte nach Element, nicht nach Datei; Duplikate im Export zählen mit.

| Element | Anzahl | Bemerkung |
|---|---|---|
| `serviceTask` extern | 162 | Topics `pessimistic` 72, `optimistic` 54, `process_io` 36 |
| `serviceTask` mit `camunda:connector` | 39 | `http-connector` 29, davon 26 Benachrichtigungen (URL wird beim Deploy eingesetzt) und 3 eigene HTTP-Aufrufe der Nutzer (`POST http://${ip}/ta`, `GET ${url}`, Ausgaben `statusCode`, `response`); `mail-send` 10 |
| `task` ohne Typ | 54 | Träger von Ein- und Ausgabe-Mappings; 4 mit `camunda:asyncBefore` |
| `scriptTask` | 15 | alle mit `camunda:resultVariable` |
| `intermediateCatchEvent` | 152 | Message 92, Timer 60 |
| `startEvent` | 63 | ohne Typ 56, Message 5 (davon 4 Geräteevents mit `senergy:*`), Timer 2 |
| `endEvent` | 20 | alle ohne Typ |
| `intermediateThrowEvent` | 3 | ohne Typ und ohne ausgehenden Fluss, also faktisch ein Endpunkt |
| `exclusiveGateway` | 87 | 33 verzweigend, 52 zusammenführend, 2 beides; Default-Flow in 1 Modell |
| `parallelGateway` | 25 | 20 verzweigend, 5 zusammenführend; in 12 Modellen |
| `inclusiveGateway` | 8 | 5 verzweigend, 3 zusammenführend; in 8 Modellen |
| `eventBasedGateway` | 17 | in 12 Modellen |
| `conditionExpression` | 80 | JavaScript 52, JUEL 28 |
| Timer `timeDuration` | 45 | ISO 8601 (`PT5S` bis `P1D`), 5× `${cycle}`, 2× `R/P1D` |
| Timer `timeCycle` | 9 | Cron mit 6 und 7 Feldern (`0 0 11 * * * ?`), 4× aus Variable (`${cron_0}`) |
| Timer `timeDate` | 8 | |
| `camunda:inputOutput` | 273 | 332 Eingabe-, 177 Ausgabeparameter; 127 `camunda:script` darin |
| Skriptsprachen | | JavaScript 129, Groovy 13; Groovy enthält ausschließlich `println`-Ausgaben |
| `camunda:formData` / `formField` | 26 / 67 | Startparameter |
| `signal` | 6 | nur als Definition auf Dateiebene, nirgends referenziert (3 Kopien eines Modells) |
| Pools, Lanes, `group` | 3 / 5 / 1 | nur Darstellung |

In Skripten verwendet werden `JSON.parse`/`stringify`, `Date` und `Math.*`. Zugriffe auf `java.*` oder `execution.*` kommen in diesen Exporten nicht vor; die Smart-Service-Fixtures nutzen `execution.setVariable`.

Nicht vorhanden sind: Boundary-Events, Subprozesse, Call Activities, User Tasks, Multi-Instance, Listener, Error-, Escalation-, Compensation-, Link-, Signal- und Terminate-Events.

### Benötigt

- **BPMN-01 (M)** Prozess mit genau einem ausführbaren `bpmn:process`. Pools, Lanes, `collaboration`, `messageFlow`, `group`/`category` und nicht referenzierte `signal`-Definitionen werden gelesen und ignoriert. Heute dienen Pools und Lanes nur der Gruppierung im Deploy-Dialog (`parser/groups.go`).
- **BPMN-02 (M)** Startereignis ohne Typ, mit Startparametern aus `camunda:formData/formField` (`id`, `label`, `type`, `defaultValue`, `camunda:properties`, darunter `ignore_on_start`, `order`, `characteristic_id`, `options`, `multiple`, `iot`, `criteria`).
- **BPMN-03 (M)** Message-Startereignis:
  - für Geräteereignisse;
  - für benannte Nachrichten mit `messageRef` (Smart-Service-Wartungsprozeduren, Start mit vorgegebener Instanz-ID).
- **BPMN-04 (M)** Timer-Startereignis.
- **BPMN-05 (M)** Endereignis ohne Typ. Ein Element ohne ausgehenden Fluss beendet seinen Token ebenfalls, wie in Camunda; das betrifft das Zwischenereignis ohne Typ (3 Vorkommen).
- **BPMN-06 (M)** Zwischenereignis Timer (catch) mit allen drei Definitionsarten:
  - `timeDuration` als ISO 8601;
  - `timeDate` als ISO-Zeitpunkt;
  - `timeCycle` als Quartz-Cron mit 6 oder 7 Feldern einschließlich `?`.
  - Der Wert darf aus einer Variablen bzw. einem Ausdruck kommen (heute `${cycle}`, `${cron_0}`); nach dem Wechsel auf JavaScript wird das ein JS-Ausdruck.
- **BPMN-07 (M)** Zwischenereignis Message (catch), siehe Abschnitt 10.
- **BPMN-08 (M)** Service Task mit Aufgabentyp. Heute wird er über `camunda:type="external"` und `camunda:topic` ausgewählt. Aufgabentypen:
  - heute in Prozessen: `optimistic`, `pessimistic`, `process_io`;
  - heute in Smart Services: `info`, `import`, `process_deployment`, `process_deployment_start`, `analytics`, `device_repository`, `watcher`, `export`.
  - Welche Typen ein Modell nutzen darf, legt sein Namespace fest (NS-03).
  - Das Topic `export` im Prozess-Designer (`IotProps.js`) ist veraltet und entfällt (entschieden). Es hat in der Haupt-Engine keinen Worker und kommt in den Exporten nicht vor.
- **BPMN-09 (M)** Aufgabentyp „Benachrichtigung“ anstelle des `http-connector` mit `deploymentIdentifier=notification`. Heute setzt process-deployment URL und Header ein (`stringifier/notification.go`).
- **BPMN-10 (M)** Aufgabentyp „HTTP-Aufruf“ für Aufrufe, die Nutzer selbst definieren: Methode, URL, Header, Body, Ausgaben `statusCode` und `response`. In 2 Modellen belegt; die URL `http://${ip}/ta` zeigt auf ein Gerät im lokalen Netz.
  - Im Fog ist der Aufgabentyp erlaubt.
  - Er ist konfigurierbar (entschieden), je Namespace (NS-04): ein- oder ausgeschaltet, dazu Freigabe- und Sperrlisten für Ziele (Hosts, Netze, Ports), getrennt für Cloud und Fog.
  - Vorgabe in der Cloud: aus oder nur mit Freigabeliste. Sonst kann ein Nutzerprozess interne Dienste im Cluster erreichen. Heute blockiert die Network-Policy der Engine ihn dort ohnehin (Egress nur zu Notifier, DB, Port 587, DNS).
  - Vorgabe im Fog: an, mit Zielen im lokalen Netz.
  - Zeitlimit und Größenlimit für die Antwort.
- **BPMN-11 (M)** Script Task, JavaScript, mit `camunda:resultVariable`.
- **BPMN-12 (M)** Exklusives Gateway: verzweigend mit Bedingungen und Default-Flow, zusammenführend ohne Bedingung.
- **BPMN-13 (M)** Ereignisbasiertes Gateway mit Message- und Timer-Ereignissen dahinter, auch mit mehreren eingehenden Flüssen (Rücksprung in die Schleife).
- **BPMN-14 (M)** Paralleles Gateway, verzweigend und zusammenführend. In den Exporten verzweigen 20 Gateways und nur 5 führen zusammen; verbreitet sind parallele Zweige, die jeweils für sich endlos laufen. Für langlebige Prozesse gilt LONG-16.
- **BPMN-15 (M)** Inklusives Gateway, verzweigend und zusammenführend, mit der Semantik von Camunda 7: Die Zusammenführung wartet auf alle Token, die sie noch erreichen können. Die Semantik muss gegen Camunda nachgetestet werden, weil sie bei Schleifen nicht offensichtlich ist.
- **BPMN-16 (M)** `bpmn:task` ohne Typ als Durchlaufknoten mit Ein- und Ausgabe-Mappings. `camunda:asyncBefore`/`asyncAfter` wird akzeptiert und ignoriert, weil die Automation-Engine jeden Schritt ohnehin persistiert.
- **BPMN-17 (M)** Ein- und Ausgabe-Mappings an Tasks und Events:
  - Wert als Text, als JavaScript-Ausdruck oder als Skript;
  - verschachtelte Zielnamen (`inputs.r`, `io.read.x`, `outputs.r`);
  - Map-Werte (`camunda:map/entry`).
- **BPMN-18 (M)** Validierung beim Speichern des Modells und beim Deploy:
  - Elemente außerhalb der unterstützten Menge und Aufgabentypen, die der Namespace nicht erlaubt (NS-03, NS-06), werden mit einer Fehlermeldung abgelehnt, die das Element benennt.
  - Heute lassen process-deployment und Designer alles durch.
  - Der Modeler im web-ui wird auf dieselbe Menge eingeschränkt, je Namespace (NS-09, Abschnitt 21).

### Nicht benötigt

User Task, Tasklist, Receive Task, Send Task und Business Rule Task; Subprozess, Call Activity, Multi-Instance; Boundary-Events, Signal (Throw und Catch), Link, Error, Escalation, Compensation, Terminate und Transaction; Conditional Event im BPMN-Sinn; Execution- und Task-Listener; `camunda:class`/`delegateExpression`; DMN, FEEL, JUEL und Groovy; Instanzmigration zwischen Versionen; Prozessinstanz-Modifikation; Mail-Versand (`mail-send`).

Zu `mail-send`: process-deployment lehnt den Connector schon heute ab (`deploymentmodel/validation.go`). Die 10 Vorkommen stehen in Modellen, die sich deshalb heute schon nicht deployen lassen.

Das „conditional event“ der Plattform ist ein Message-Ereignis mit `senergy:script`. Die Bedingung wertet der event-worker aus, nicht die Engine.

---

## 4. Ausdrücke und Skripte

- **EXPR-01 (M)** Einzige Ausdrucks- und Skriptsprache ist JavaScript. Das gilt für:
  - Bedingungen an Sequenzflüssen;
  - Ein- und Ausgabe-Mappings;
  - Timer-Werte;
  - Script Tasks;
  - Event-Bedingungen.

  Modelle mit `${...}` oder einer anderen Sprache werden beim Speichern abgelehnt; bestehende Modelle werden bei der Migration umgeschrieben (MIG-04). In Go bietet sich goja an, das der event-worker schon nutzt.
- **EXPR-02 (M)** Ein Mapping-Wert ist entweder Text oder ein Ausdruck, und zwar ausdrücklich. Heute entscheidet allein das Vorkommen von `${`. Für Text mit eingebetteten Werten (`http://${ip}/ta`) wird ein Template-Literal verwendet.
- **EXPR-03 (M)** Sandbox:
  - Zeitlimit je Auswertung (der event-worker nutzt 2 s);
  - Speicherlimit;
  - kein Zugriff auf Engine-Interna, Dateisystem oder Netz.
  - Heute ist der einzige Schutz das Präfix `var java = {}; var execution = {};` im Wrapper; dazu lehnt process-deployment `execution.` ab.
- **EXPR-04 (M)** Definierte Funktionen statt Engine-Objekten:
  - `setVariable(name, value)` für Smart Services; heute rufen sie `execution.setVariable` auf, weil das Repository am Wrapper vorbei deployt;
  - `log(...)` für Debug-Ausgaben, sichtbar im Debugging (Abschnitt 13). Er ersetzt die Groovy-`println`-Skripte, die heute nur ins Container-Log schreiben.
- **EXPR-05 (M)** Wertsemantik kompatibel zu dem, was bestehende Skripte erwarten:
  - JSON-Strings, die Skripte mit `JSON.parse` lesen;
  - `result` als Liste ab Payload-Version 2 (`result[0]`);
  - Zahlen als Double;
  - `null` für fehlende Werte (`distance_km == null`).
  - Camunda liefert heute Java-Objekte (Spin, ArrayList); Abweichungen werden mit den Migrationstests gefunden (MIG-04).

---

## 5. Datenmodell und Prozessmodelle

### Datenmodell

- **DATA-01 (M)** Es gibt drei Begriffe: **Modell**, **Deployment** und **Instanz**.
  - Die **Prozessdefinition** entfällt (entschieden). Camunda braucht sie, weil ein Deployment dort mehrere Ressourcen enthalten kann und nicht unbedingt einen Prozess.
  - Bei uns enthält ein Deployment genau einen ausführbaren Prozess (BPMN-01). Heute vergibt der Wrapper für weitere Prozesse eines Modells Suffixe (`deplid_<id>_i`); das entfällt mit BPMN-01.
- **DATA-02 (M)** Beziehungen:

  | Begriff | Unterteilt in | Verweist auf |
  |---|---|---|
  | Modell | Versionen (MOD-05) | – |
  | Deployment | Versionen (DEP-04) | Modell und Modellversion, sofern vorhanden |
  | Instanz | Läufe (RUN-11) | Deployment, dazu Modell, sofern vorhanden |
  | Lauf | – | die Deployment-Version, auf der er läuft |

  - **Deployment ohne Modell:** Das sind z. B. Prozesse, die Widgets erzeugen (`source=generated`), und Deployments, die per API mit eigenem XML angelegt werden.
  - **Modellbezug der Instanz:** Er wird aus dem Deployment übernommen und an der Instanz gespeichert, damit „alle Instanzen eines Modells“ ohne Umweg abfragbar sind.
  - **Version je Lauf:** Die Deployment-Version hängt am Lauf, nicht an der Instanz. Ein Neustart beim Update (DEP-04) beginnt einen neuen Lauf auf der neuen Version, unter derselben Instanz-ID.
- **DATA-03 (M)** Alles, was heute an der Prozessdefinition hängt, hängt am Deployment bzw. an der Deployment-Version:
  - Incident-Behandlung;
  - Bindung von IO-Werten;
  - Eventbeschreibungen, Timer und Zeitpläne;
  - Aufbewahrungseinstellung;
  - Metrik-Labels;
  - Kommando-Metadaten.
- **DATA-04 (M)** Neue Namen statt Definitions-IDs:
  - **API:** Die neue API kennt nur `deployment_id`. `processDefinitionId`, `process_definition_id`, `processDefinitionNameLike` und die Routen `/v2/process-definitions/...` gibt es nur unter `deprecated/` (API-02).
  - **Nachrichten an Connectoren:** `task_info` (`platform-connector-lib`) bekommt `deployment_id`. Bis alle Connectoren die neue Bibliothek nutzen, füllt die Engine zusätzlich `process_definition_id`, danach entfällt es.
  - **Metriken:** Das Label für den cost-calculator heißt `deployment_id`. Engine und cost-calculator werden gemeinsam umgestellt (TASK-12).
  - **Modellinhalte:** Platzhalter `{{DefinitionId}}` in Process-IO-Schlüsseln werden bei der Migration auf `{{DeploymentId}}` umgeschrieben (MIG-04).

### Namespaces

- **NS-01 (M)** Normale Prozesse und Smart Services sind im Hintergrund dasselbe (entschieden): Modelle, Deployments, Instanzen und Läufe mit denselben Funktionen.
  - Jedes Modell, jedes Deployment und jede Instanz gehört zu genau einem **Namespace**.
  - Auflistungen, Suche und Zählungen sind je Namespace getrennt.
- **NS-02 (M)** Namespaces sind frei konfigurierbar und erweiterbar, nicht auf `processes` und `smart-services` beschränkt (entschieden).
  - Anlegen und Ändern geht über Konfiguration bzw. eine Admin-API, ohne Codeänderung.
  - Ausgeliefert werden `processes` und `smart-services`, mit dem heutigen Verhalten.
- **NS-03 (M)** Je Namespace ist konfigurierbar, welche Module bzw. Worker, also Aufgabentypen (TASK-00), erlaubt oder verboten sind (entschieden), als Freigabe- oder Sperrliste. Beispiel nach heutigem Stand:
  - `processes` erlaubt Gerätekommando, Process-IO, Benachrichtigung und HTTP-Aufruf;
  - `smart-services` erlaubt die Module aus TASK-20 bis TASK-27.

  Geprüft wird:
  - beim Speichern eines Modells, als Hinweis (MOD-04);
  - beim Deploy, als Ablehnung (BPMN-18);
  - bei der Ausführung: Ein inzwischen verbotener Typ erzeugt einen Incident. Bestehende Deployments, die nach einer Konfigurationsänderung nicht mehr passen, werden gekennzeichnet.
- **NS-04 (M)** Die Einstellungen des HTTP-Aufrufs (BPMN-10) gelten je Namespace, getrennt für Cloud und Fog.
- **NS-05 (M)** Rechte über **eigene Topics in permissions-v2 je Namespace und Ebene** (entschieden), für Modelle, Deployments und Instanzen.
  - Beim heutigen Stand mit `processes` und `smart-services` sind das **6 neue Topics**. Namensvorschlag: `<namespace>_models`, `<namespace>_deployments`, `<namespace>_instances`.
  - Die **Default-Permissions** werden je Namespace konfiguriert (entschieden). Technisch sind sie die `DefaultPermissions` des jeweiligen Topics in permissions-v2 (`model.Topic.DefaultPermissions`): Diese Rechte erhalten Besitzer, Gruppen oder Rollen beim Anlegen einer Ressource.
  - Jede Ebene ist teilbar, mit Lesen, Schreiben, Ausführen und Administrieren.
  - Unabhängig von den Default-Permissions hat der Owner immer Administrieren (SEC-10).
  - Vorgaben, die das heutige Verhalten nachbilden:
    - `processes`: Modelle teilbar, Deployments und Instanzen nur für den Besitzer;
    - `smart-services`: Modelle (Designs) nur für den Besitzer, Deployments (Releases) und Instanzen teilbar.
  - Wird ein Namespace angelegt, legt die Engine seine drei Topics über die Admin-API von permissions-v2 an (`SetTopic`). Ändert sich die Namespace-Konfiguration, aktualisiert sie die `DefaultPermissions` der Topics. Wird ein Namespace entfernt, werden seine Topics entfernt, sobald er keine Ressourcen mehr hat.
  - Die heutigen Topics `processmodel`, `smart_service_releases` und `smart_service_instances` gehen in die neuen auf (MIG-08).
- **NS-06 (M)** Die Erlaubnis bzw. das Verbot von BPMN-Elementen ist am Namespace konfigurierbar (entschieden), als Einschränkung der Menge aus Abschnitt 3. Für `smart-services` wird die heutige Regel übernommen: keine Timer-, Signal- und Conditional-Startereignisse (`smart-service-repository/pkg/controller/validation.go`).
- **NS-07 (S)** Weitere Vorgaben je Namespace:
  - Vorgabe und Obergrenze der Aufbewahrung (HIST-04);
  - Incident-Behandlung;
  - Update-Strategie;
  - Grenzen aus Abschnitt 12.
- **NS-08 (M)** Namespace-übergreifende Beziehungen werden ausdrücklich erlaubt (entschieden). Beispiel: Eine Instanz in `smart-services` legt über ein Modul ein Deployment in `processes` an.
  - Welche Namespaces ein Namespace so ansprechen darf, ist konfigurierbar.
  - Das angelegte Deployment verweist auf die Instanz, die es verwaltet. Das gilt für Löschkaskade und Update-Strategie (SMS-03).
- **NS-09 (M)** Es gibt einen Designer für alle Namespaces (entschieden). Er richtet Palette und Eigenschaften nach dem Namespace aus: nur erlaubte Aufgabentypen und Elemente (NS-03, NS-06), nur angebotene Formen der Geräteauswahl (NS-11).
- **NS-10 (M)** Namespaces gelten auch auf dem Gateway. Die Konfiguration wird mit den Deployments repliziert.
- **NS-11 (M)** Für die Geräteauswahl sind beide Wege möglich (entschieden):
  - **beim Deploy**, je Task und Event (heute Prozesse, DEP-01);
  - **über Startparameter** einer Instanz, mit Kriterien und Optionen (heute Smart Services, SMS-08).

  Ein Modell kann beides mischen. Am Namespace ist konfigurierbar, welcher Weg erlaubt und angeboten wird; Designer, Deploy-Dialog und Start-Dialog richten sich danach.

### Prozessmodelle (bisher process-model-repository)

- **MOD-01 (M)** Modelle speichern: BPMN-XML, SVG, Name, Beschreibung, Besitzer, Namespace, Datum, letzte Änderung.
  - Auflisten mit Paging, Suche, Sortierung, ID-Liste, Besitzerfilter und Rechtefilter, also im fachlichen Umfang von heute `/v2/processes`.
  - Die heutigen Routen `/processes`, `/v2/processes` und `/processes/{id}/publish` gibt es nur unter `deprecated/` (API-02).
- **MOD-02 (M)** Rechte über das Modell-Topic des jeweiligen Namespace in permissions-v2 (NS-05), mit Lesen, Schreiben, Ausführen und Administrieren.
  - process-deployment prüft heute beim Vorbereiten über `p=x`, ob der Nutzer das Modell ausführen darf. Diese Prüfung bleibt.
  - Das Aufräumen alle 6 h, das Rechte- und Modellbestand abgleicht, entfällt, sobald beides transaktional gemeinsam geschrieben wird oder die Engine die Rechte führt.
- **MOD-03 (M)** Veröffentlichen (`publish` mit Beschreibung) bleibt erhalten.
- **MOD-04 (M)** Beim Speichern wird dieselbe Validierung angewendet wie beim Deploy (BPMN-18, EXPR-01). Ein Modell, das sich nicht deployen lässt, fällt schon beim Speichern auf, mit einem Hinweis statt einer Ablehnung, damit Zwischenstände speicherbar bleiben.
- **MOD-05 (M)** Modelle sind versioniert. Jedes Speichern erzeugt eine Version, und ein Deployment hält Modell-ID und Modellversion fest. Heute speichert das Deployment keine Modell-ID; Zuordnungen gehen nur über Name und Mandant (Vault: „Every tenant's deployments are only visible in the engine“).
- **MOD-06 (S)** Je Modell sind die zugehörigen Deployments abrufbar, mit Versionsabstand. Nach einer Modelländerung kann der Nutzer die Deployments aktualisieren und wählt dabei den Umgang mit laufenden Instanzen (DEP-04).
- **MOD-07 (S)** Aufbewahrung alter Modellversionen: Versionen, auf die ein Deployment verweist, bleiben erhalten. Sonst gilt eine Begrenzung nach Anzahl.
- **MOD-08 (M)** Beim Löschen eines Nutzers werden seine Modelle samt Rechten entfernt (SEC-08). Fremde Deployments, die auf einem solchen Modell beruhen, behalten ihre Kopie des BPMN in der Deployment-Version; ihr Modellverweis wird als gelöscht gekennzeichnet. Laut Vault-Notiz „Deleting a user leaves process models and releases behind“ blieben in Prod Reste stehen.
- **MOD-09 (K)** Kafka `processmodel` als `PublishToKafkaTopic` des permissions-v2-Topics. Gefunden wurde als Konsument nur permission-search, das in `rancher-2-defs` nicht mehr deployt ist. Das Topic entfällt, wenn sich kein weiterer Konsument findet.

---

## 6. Deployment

Integriert process-deployment, process-fog-deployment, event-deployment und den Deploy-Teil des Wrappers.

- **DEP-01 (M)** Die Vorbereitungs-API bleibt fachlich erhalten:
  - `start-parameters`;
  - `prepared-deployments`: BPMN parsen, Auswahloptionen über device-selection, Import-Typen;
  - `deployments` mit CRUD;
  - `dependencies`.

  Konsumenten sind das web-ui, generierte Widget-Prozesse (`source=generated`) und canary. Der Round-Trip über smart-service-module-worker-process wird mit dessen Integration intern.
- **DEP-02 (M)** **Eine ID pro Deployment.** Die Deployment-UUID ist zugleich die Engine-ID. Damit entfallen:
  - die vid-Tabelle;
  - die Wiederhol-Schleife bei `UnknownVid`;
  - das Präfix `deplid_`;
  - im Fog die asynchrone Rückmeldung der Camunda-ID und der `/metadata`-Endpunkt.
- **DEP-03 (M)** Atomarer Deploy, der in einem Schritt zusammenfasst, was heute auf drei Dienste verteilt ist:
  - BPMN, Deployment-Modell, Abhängigkeiten (Geräte, Events);
  - Eventbeschreibungen;
  - Incident-Behandlung;
  - Aufbewahrungseinstellungen;
  - Zeitpläne.

  Heute meldet `SetDeployment` Erfolg, auch wenn die Synchronisation scheitert, und die Sync-Schleife löscht das Deployment danach.
- **DEP-04 (M)** Beim Update wählt der Nutzer, was mit laufenden Instanzen geschieht:
  - **stoppen**: heutiges Verhalten, nur jetzt angekündigt;
  - **auslaufen lassen**: Laufende Instanzen bleiben auf ihrer Version, neue Starts nutzen die neue;
  - **neu starten**: stoppen und unter derselben Instanz-ID mit den gespeicherten Startparametern einen neuen Lauf auf der neuen Version beginnen (RUN-11).

  Daraus folgen:
  - **Versionen je Deployment.** Mehrere Versionen laufen gleichzeitig; eine alte Version wird entfernt, wenn ihre letzte Instanz endet. Eventbeschreibungen, Zeitpläne und Incident-Einstellungen hängen an der Version.
  - **Einheitliche Wahl über alle Wege.** Die Wahl steht in der Update-API, im web-ui und bei Updates über Smart Services (SMS-03).
  - **Stabile Event-IDs über Updates hinweg.** Heute bekommt jedes Event bei jedem Update eine neue `event_id`.
- **DEP-05 (M)** Die Gerätezuordnung steht typisiert am Task im Deployment: Gerät, Service, Gruppe, Funktion, Aspekte, Characteristic, Pfade, Configurables, `prefer_event`. Heute liegt sie als JSON-String in der Prozessvariablen `payload`.
- **DEP-06 (M)** Rechteprüfung beim Deploy auf die ausgewählten Geräte, Gerätegruppen und Imports über permissions-v2, wie heute in `EnsureDeploymentSelectionAccess`.
- **DEP-07 (M)** Eventbeschreibungen werden beim Deploy erzeugt und als Teil der Deployment-Version gespeichert:
  - Gruppen-Expansion auf Gerät × Service;
  - Gerät ohne Service auf alle passenden Services;
  - Import-Events.
- **DEP-08 (M)** Ändert sich eine Gerätegruppe (Kafka `device-groups`), werden die betroffenen Beschreibungen neu berechnet. Danach werden gezielt nur die betroffenen Einträge im event-worker invalidiert; heute leert jedes Deployment die Caches aller event-worker-Pods.
- **DEP-09 (M)** Ziel des Deployments: Cloud oder ein Gateway (`networkId`), über dieselbe API.
- **DEP-10 (M)** Signal „Deployment geändert/gelöscht“ für den event-worker (heute Kafka `process-deployment-done`).
- **DEP-11 (M)** Das Löschen kaskadiert in einer Transaktion über alle Versionen:
  - Instanzen, Historie, IO-Variablen;
  - Incidents und Incident-Handler;
  - Eventbeschreibungen, Zeitpläne, Abhängigkeiten, Debug-Verlauf.

---

## 7. Instanzen und Laufzeit

- **RUN-01 (M)** Instanz eines Deployments starten, mit typisierten Variablen, auf der aktuellen oder einer bestimmten Deployment-Version.
  - **Die ID ist stabil und kann vom Aufrufer vorgegeben werden.** Ohne Vorgabe erzeugt die Engine eine UUID.
  - Mit Vorgabe ist der Start idempotent (`PUT /process-instances/{id}`): Ein zweiter Start mit derselben ID und demselben Inhalt liefert die bestehende Instanz statt einer zweiten; mit abweichendem Inhalt gibt es einen Konflikt.
  - IDs sind je Mandant eindeutig. Empfohlen sind UUIDs.
  - Die Startvariablen werden an der Instanz gespeichert, für Restart und Debugging.
  - **Business Keys entfallen** (entschieden), so wie beim Deployment die vid entfällt (DEP-02). Wofür sie heute dienen und was sie ersetzt:
    - Smart Services: Instanz-ID des Smart Service als Business Key → wird die Prozess-Instanz-ID.
    - Wartungsprozeduren: Wartungs-ID als Business Key → wird die ID der Instanz, die die Prozedur startet.
    - Restart nach Incident: neue Instanz mit gleichem Key → neuer Lauf derselben Instanz (RUN-11).
    - Fog: Präfix `wardened:` für Neustarts aus der Cloud → entfällt, ein Neustart ist ein neuer Lauf.
    - Smart-Service-Modul `process_deployment_start`: Zustandsprüfung über die Historie nach Key → Abfrage nach Instanz-ID.
    - Filter im Monitor (`processInstanceBusinessKey`) → Filter nach Instanz-ID.
- **RUN-02 (M)** Startparameter abfragen (`/parameter`), ohne Felder mit `ignore_on_start`.
- **RUN-03 (M)** Laufende Instanzen auflisten, zählen und je Deployment anzeigen, **mit Paging**. Heute gibt es kein Paging.
- **RUN-04 (M)** Instanzen stoppen, einzeln und mehrere. Ein Stopp beendet den laufenden Lauf; die Historie bleibt.
- **RUN-05 (M)** Variable einer laufenden Instanz setzen (`PUT .../variables/{name}`).
- **RUN-06 (M)** Persistente Timer mit Wiederaufnahme nach Neustart. Ein Timer feuert genau einmal; das gilt auch, wenn mehrere Replicas laufen.
- **RUN-07 (M)** Zeitgesteuerte Starts, übernommen aus dem process-scheduler:
  - beliebig viele Zeitpläne je Deployment (1:n). So ist es heute schon: Jeder Eintrag verweist auf ein `process_deployment_id`, und mehrere Einträge dürfen dasselbe Deployment nennen;
  - je Zeitplan Cron mit optionalem Sekundenfeld, Alias, aktiv/inaktiv;
  - ein Zeitplan hängt am Deployment, nicht an einer Version, und startet nach einem Update die aktuelle Version;
  - jeder Start erzeugt eine neue Instanz mit einer von der Engine vergebenen ID (RUN-01);
  - Zeitpläne werden beim Löschen eines Nutzers mit entfernt (SEC-08). Der heutige Aufruf `/schedules` aus user-management geht im Aufruf aus SEC-14 auf.
  - Ausführung genau einmal.
- **RUN-08 (M)** Zustandsübergänge laufen transaktional und überstehen einen Neustart.
- **RUN-09 (M)** Eine Instanz-ID hat höchstens einen aktiven Lauf. Damit entfällt der heutige Fall, dass zwei Restart-Ebenen im Fog zwei Instanzen mit demselben Business Key erzeugen.
- **RUN-10 (M)** Instanzen sind in ihrer Laufzeit nicht begrenzt. Endlosschleifen auf Sensordaten sind ein Haupteinsatzfall; die Anforderungen an den Speicher stehen in Abschnitt 12.
- **RUN-11 (M)** Läufe: Eine Instanz-ID kann nacheinander mehrere Läufe haben, mit fortlaufender Laufnummer. Jeder Lauf hält die Deployment-Version fest, auf der er läuft (DATA-02).
  - Ein neuer Lauf entsteht durch Restart nach Incident (INC-02), durch Neustart beim Update (DEP-04), bei der Migration (MIG-02) und durch einen ausdrücklichen Neustart über die API.
  - Historie, Incidents und Debug-Verlauf sind je Lauf getrennt und unter der Instanz-ID gemeinsam abrufbar. Im Debugging lässt sich damit verfolgen, wie oft eine Instanz neu gestartet wurde und warum.
  - Ein neuer Lauf beginnt am Startereignis mit den gespeicherten Startvariablen; Prozessvariablen des vorigen Laufs gehen nicht über.
- **RUN-12 (M)** Business Keys gibt es in der neuen API nicht. Für bestehende Clients gilt:
  - `business_key` beim Start und `DELETE .../process-instances-by-business-key/{key}` gibt es nur unter `deprecated/` (API-02). Dort wird der Business Key auf die Instanz-ID abgebildet. Ist die ID schon vergeben, gilt das idempotente Verhalten aus RUN-01: Wer bisher mehrere Instanzen mit demselben Key gestartet hat, bekommt einen Konflikt statt einer weiteren Instanz.
  - `business_key` in `task_info` (`platform-connector-lib`) bleibt leer und entfällt mit der Umstellung der Connectoren (DATA-04).

---

## 8. Aufgabenausführung (integrierte Worker)

Übernimmt `external-task-worker` (optimistic, pessimistic), `process-io-worker`, den Fog-Worker und die acht Smart-Service-Module-Worker. Eine externe Task-API für Dritte gibt es nicht.

- **TASK-00 (M)** Aufgabentypen sind eine interne Schnittstelle der Engine (Go-Interface). Jeder Typ:
  - bekommt Eingaben und Zugangsdaten des Prozessbesitzers;
  - liefert Ausgaben oder einen Fehler;
  - kann asynchron auf eine Antwort warten, deren Zustand die Engine persistiert;
  - bekommt Timeout und Wiederholungen von der Engine.

  Neue Typen sind Code in der Engine, kein Netzprotokoll. Welche Typen erlaubt sind, legt der Namespace fest (NS-03).

### Gerätekommandos

- **TASK-01 (M)** Aufgabentyp „Gerätekommando“ mit zwei Abschlussstrategien:
  - **optimistisch**: abschließen, dann senden, ohne auf eine Antwort zu warten;
  - **pessimistisch**: senden, auf die Antwort warten, dann abschließen.
- **TASK-02 (M)** Gerät, Service und Protokoll auflösen:
  - über die device-repository (`/devices`, `/device-types`, `/protocols`, `/device-groups`);
  - mit dem Token des Prozessbesitzers (Token Exchange);
  - mit einem Cache, der über Kafka `devices`, `device-types`, `protocols` und `device-groups` invalidiert wird.
- **TASK-03 (M)** Marshalling über den Marshaller (v2) oder im Prozess selbst. Im Fog läuft es im Prozess, weil es dort keinen Marshaller-Dienst gibt.
- **TASK-04 (M)** Versand als `ProtocolMsg`:
  - auf das Kafka-Topic des Protokoll-Handlers (`connector`, `mqtt`, `nimbus` ...), mit der Geräte-ID als Schlüssel;
  - per HTTP, wenn der Handler eine URL ist;
  - im Fog über MQTT `command/{localDeviceId}/{localServiceId}`.
- **TASK-05 (M)** Antworten empfangen:
  - über Kafka `response` und `errors`;
  - per HTTP-Callback (heute `response_to`, `POST /responses`);
  - im Fog über MQTT `response/#`.

  Der Vertrag `task_info` (`task_id`, `process_instance_id`, `process_definition_id`, `tenant_id`, `completion_strategy` ...) bleibt erhalten, denn die Connectoren spiegeln ihn zurück (`platform-connector-lib`). `process_definition_id` trägt die Deployment-ID (DATA-04).
- **TASK-06 (M)** Mehrgeräte-Aufgaben:
  - Gerätegruppe aufgelöst in Gerät × passenden Service (Funktion plus alle Aspekte);
  - Reihenfolge parallel, sequenziell oder Round-Robin;
  - Teilaufgaben-IDs;
  - Ergebnis als Liste.

  Den Zustand der Teilaufgaben hält heute ein einzelner memcached; künftig liegt er **in der Engine-Persistenz**.
- **TASK-07 (M)** Timeout und Wiederholungen sind ausdrücklich konfigurierbar, je Task über `retries`. Heute wirkt die Camunda-Lock-Dauer (120 s) als Antwort-Timeout.
- **TASK-08 (M)** Wert lesen statt Kommando senden (`prefer_event`, Services mit Interaktion `EVENT`):
  - Abfrage bei timescale-wrapper `/queries`, im Fog bei mgw-last-value;
  - das Ergebnis wird wie eine Antwort behandelt.
- **TASK-09 (M)** Laufzeit-Eingaben wie bisher:
  - `inputs` und `inputs.<pfad>` mit Typumwandlung;
  - `overwrite` ersetzt Gerät, Service und Protokoll;
  - `incident` löst einen Incident durch den Nutzer aus.
- **TASK-10 (M)** Das Ergebnis steht als lokale Variable `result` bereit (Liste ab Payload-Version 2) und läuft danach durch die Ausgabe-Mappings.
- **TASK-11 (M)** Jeder Task wird höchstens einmal abgeschlossen, auch wenn Antworten doppelt oder verspätet eintreffen.
- **TASK-12 (M)** Kostenrelevante Metrik mit gleicher Label-Semantik: `external_task_worker_task_command_send_count_vec{user_id, process_definition_id}`, wobei `process_definition_id` die Deployment-ID trägt (DATA-04). Der cost-calculator verteilt darüber die Prozesskosten auf Nutzer.

### Process-IO

- **IO-01 (M)** Aufgabentyp „Process-IO“ mit den Eingabekonventionen:
  - `io.read.<var>` liest einen Wert;
  - `io.write.<key>` schreibt einen Wert, JSON wird geparst;
  - `io.default.<key>` gibt einen Default vor;
  - Platzhalter `{{InstanceId}}` und `{{DeploymentId}}`. `{{DefinitionId}}` wird bei der Migration umgeschrieben (DATA-04).
- **IO-02 (M)** Persistenter Key-Value-Speicher je Nutzer, optional an Instanz oder Deployment gebunden.
  - Lesen, Schreiben, Löschen, Zählen, Bulk;
  - Filter nach Schlüssel (regulärer Ausdruck), `deployment_id` und `instance_id`.
  - Die heutigen Routen (`/variables`, `/values`, `/bulk`) und der Filter `process_definition_id` gibt es nur unter `deprecated/`.
- **IO-03 (M)** An eine Instanz gebundene Werte werden mit der Instanz gelöscht; die Aufbewahrung folgt der Historie. Heute bleiben sie, bis die Historie ausdrücklich gelöscht wird.

### Module (bisher Smart-Service-Module-Worker)

Heute laufen acht eigenständige Worker an der smart-service-engine, alle über `smart-service-module-worker-lib`. Sie schreiben Modulinformationen an das smart-service-repository und melden Fehler dorthin; Camundas Incident-Mechanismus nutzen sie nicht. Mit der Integration des Repositorys (Abschnitt 18) werden diese Aufrufe intern. Welche dieser Aufgabentypen ein Modell nutzen darf, legt sein Namespace fest (NS-03).

- **TASK-20 (M)** Aufgabentyp `process_deployment`: Prozess deployen oder aktualisieren, in der Cloud oder auf einem Gateway, und das Modul im Repository registrieren. Heute holt der Worker ein vorbereitetes Deployment und schickt es zurück. Integriert wird daraus ein interner Aufruf, und der Feldverlust beim Round-Trip über alte Modellversionen entfällt (Vault: Process-Deployment).
- **TASK-21 (M)** Aufgabentyp `process_deployment_start`: Instanz eines Deployments starten (Cloud oder Gateway) und ihren Zustand prüfen.
- **TASK-22 (M)** Aufgabentyp `analytics`: Analytics-Pipeline anlegen (flow-engine, flow-parser, import-deploy, device-repository).
- **TASK-23 (M)** Aufgabentyp `import`: Import anlegen (import-deploy).
- **TASK-24 (M)** Aufgabentyp `export`: Export anlegen (serving).
- **TASK-25 (M)** Aufgabentyp `device_repository`: Gerätegruppen anlegen (device-repository, device-manager, device-selection).
- **TASK-26 (M)** Aufgabentyp `info`: Modulinformationen schreiben.
- **TASK-27 (M)** Aufgabentyp `watcher`:
  - Beobachtet Geräteauswahlen bzw. HTTP-Ziele auf Änderungen und startet bei einer Änderung eine Wartungsprozedur.
  - Heute hat er eine eigene Mongo (`watcher-db`) und eine `ADVERTISED_URL`. Integriert wird daraus eine wiederkehrende Aufgabe in der Engine-Persistenz.
- **TASK-28 (M)** Für alle Module gilt:
  - Modul-Lebenszyklus an der Instanz (SMS-12), intern statt über das Repository;
  - Fehler als Incident der Instanz, sichtbar am Instanzstatus (SMS-11);
  - Zugangsdaten des Instanzbesitzers über Token Exchange.
- **TASK-29 (M)** Jeder bisherige Worker hat eigene Konfigurationswerte (URLs, `WORKER_PARAM_PREFIX`, `ALLOW_EVENTS_IN_FOG_PROCESSES`, `ALLOW_GENERIC_WATCH_REQUESTS`). Sie werden zu Konfigurationswerten der Engine.

---

## 9. Incidents

- **INC-01 (M)** Incidents entstehen direkt in der Engine:
  - aus fehlgeschlagenen Aufgaben;
  - aus Skriptfehlern;
  - aus Timer-Fehlern;
  - aus Fehlern beim Abschließen;
  - aus Grenzverletzungen (LONG-08, LONG-16).

  Heute gibt es drei Wege: HTTP aus dem Worker, Polling von `/engine-rest/incident` alle 5 s und Kafka `camunda_incident` (ungenutzt, Altlast).
- **INC-02 (M)** Behandlung je Deployment (`incident_handling`), wie heute:
  - die Instanz stoppen;
  - bei `notify` eine Benachrichtigung an den Notifier und an developer-notifications;
  - bei `restart` ein neuer Lauf derselben Instanz (RUN-11).

  Die Vorgabe bleibt `restart: false, notify: true`.
- **INC-03 (M)** Restart auch für Prozesse mit Startparametern, mit den gespeicherten Startvariablen (RUN-01). Heute schlägt das fehl (`restart of processes with start-parameters not supported`).
- **INC-04 (M)** Schutz vor Restart-Schleifen:
  - Begrenzung je Deployment, persistent;
  - Backoff;
  - ab einer Schwelle wird das Deployment als fehlerhaft markiert.

  Heute gibt es 5 min Sperre je Instanz in der Cloud und im Fog 3 Neustarts pro Stunde, nur im Speicher. Eine Restart-Schleife hat in Prod einen dauerhaften Fehler verdeckt (32 Neustarts an einem Tag, Vault-Notiz).
- **INC-05 (M)** Incidents auflisten mit Filter nach Namespace, Instanz, Lauf, Deployment und Element, Sortierung und Paging. Das ist der fachliche Umfang, den web-ui und Dashboard-Widgets heute über `/incidents` nutzen; die alte Route gibt es nur unter `deprecated/`.
- **INC-06 (M)** Zu jedem Incident werden Element, Fehlermeldung, Zeitpunkt und der Variablenstand zum Fehlerzeitpunkt gespeichert. Die Aufbewahrung folgt HIST-04.

---

## 10. Ereignisse und Message-Korrelation

### Was heute passiert

- Der event-worker liest **alle** Gerätedaten-Topics (`urn_infai_ses_service_.*`, `urn_infai_ses_import_.*`).
- Je Nachricht sucht er die passenden Beschreibungen nach (device, service) bzw. import.
- Er wertet `senergy:script` in goja aus.
- Danach ruft er über den Wrapper `POST /message` auf, mit `messageName = event_id`, `all: true`, `tenantId` und den lokalen Variablen `event`, `age`, `msg_id`.

### Entscheidung: Variante A

Der event-worker bleibt eine eigene Rolle, gern als zweite Betriebsart desselben Binaries:
- Er skaliert weiter getrennt über die Topic-Verteilung.
- Er liest die Eventbeschreibungen aus der Engine (API oder Änderungsstrom).
- Er ruft die Korrelations-API der Engine auf.
- event-deployment wird in den Deploy integriert (DEP-07).

### Anforderungen

- **EVT-01 (M)** Korrelation über den Nachrichtennamen an alle wartenden Catch-Events und Message-Starts eines Mandanten (`all`), mit lokalen Variablen.
- **EVT-02 (M)** Korrelation über Nachrichtenname **an eine bestimmte Instanz-ID**, z. B. für `deployment_done_<id>`. Wartungsprozeduren sind ein Message-Start mit vorgegebener Instanz-ID (BPMN-03).
- **EVT-03 (M)** Eventquellen:
  - Gerät + Service;
  - Gerät ohne Service, beim Deploy expandiert;
  - Gerätegruppe, beim Deploy expandiert;
  - Import.

  Die Analytics-Events (`MessageEvent` mit flow_id) sind tot und entfallen.
- **EVT-04 (M)** Bedingungsauswertung im event-worker:
  - Unmarshalling auf die Characteristic des Events, über Pfad oder Funktion + Aspekt;
  - Skript mit `value_variable` und `variables`;
  - ausgelöst wird bei true, „true“ oder einer Zahl ≥ 1.
- **EVT-05 (M)** QoS je Event wie heute (at-most-once oder at-least-once), dazu ein **eindeutiger Idempotenzschlüssel** je Nachricht. `msg_id` ist heute `partition:offset` ohne Topic und damit nicht eindeutig.
- **EVT-06 (M)** Nachrichten über einem Höchstalter werden verworfen (heute 1 h).
- **EVT-07 (M)** Fehler bei der Auswertung gehen gedrosselt an den Nutzer (heute über den Notifier mit einem Duplikatfenster von 1 h) und blockieren den Verbrauch nicht.
- **EVT-08 (S)** Eine Korrelation, die keine wartende Instanz findet, ist kein Fehler; sie wird als Metrik gezählt.
- **EVT-09 (M)** Der event-worker meldet nichts für das Debugging (entschieden). Seine Last bleibt unverändert. Die Engine speichert zu Events nur, was ohnehin bei ihr ankommt (HIST-05):
  - Beginn und Ende des Wartens auf ein Event;
  - jede Korrelation mit Zeitpunkt, Nachrichten-ID und den Variablen `event` und `age`.
- **EVT-10 (M)** Die Bedingungsauswertungen, die **nicht** ausgelöst haben, werden für das Debugging bei Bedarf aus den Timescale-Daten rekonstruiert:
  - Die Debug-Ansicht fragt für den Wartezeitraum (Beginn des Wartens bis zur Korrelation bzw. bis jetzt) die Werte der Event-Quelle über timescale-wrapper `/queries` ab, mit dem Token des Besitzers.
  - Bei Gruppen-Events wird je expandiertem Gerät × Service abgefragt.
  - Auf jeden Wert wendet sie dieselbe Auswertung an wie der event-worker: Unmarshalling, Umrechnung auf die Characteristic, Bedingungsskript mit `value_variable` und `variables`.
  - Grundlage ist die Eventbeschreibung der Deployment-Version, die zu diesem Zeitpunkt galt.
  - Je Wert zeigt sie Zeitpunkt, Rohwert, umgerechneten Wert, Ergebnis, Fehler und Skriptausgaben.
  - Die Auswertung nutzt denselben Code wie der event-worker (gemeinsame Bibliothek), damit Rekonstruktion und Betrieb nicht auseinanderlaufen.
  - Kosten entstehen nur, wenn jemand debuggt; die Abfrage ist seitenweise und je Anfrage zeitlich begrenzt.
- **EVT-11 (M)** Rekonstruktion und tatsächliche Korrelationen werden nebeneinander gezeigt, und die Rekonstruktion ist als solche gekennzeichnet. Abweichungen sind ein Diagnosehinweis, etwa „hätte ausgelöst, aber keine Korrelation“. Mögliche Ursachen, die die Ansicht nennt:
  - Nachricht älter als das Höchstalter (EVT-06);
  - verworfen bei `qos=0`;
  - event-worker gestört;
  - Skript nicht deterministisch, etwa durch `Date`.
- **EVT-12 (M)** Grenzen der Rekonstruktion:
  - Sie reicht so weit zurück, wie Timescale die Daten hält. Die Aufbewahrung je Tabelle regelt timescale-rule-manager; es gibt eine Vorlage `retention-one-year`, auf welche Tabellen sie angewendet wird, ist nicht geprüft.
  - Fehlen Werte in Timescale, zeigt die Ansicht nur die tatsächlichen Korrelationen.
  - Für **Import-Events** ist zu prüfen, ob Importdaten in Timescale landen. Wenn nicht, bleibt es bei den Korrelationen.
- **EVT-13 (M)** Fog-Prozesse:
  - Geräteevents auf dem Gateway gehen über mgw-device-cloud-connector auch in die Cloud (`handler/message_hdl/device_event*`) und landen damit in Timescale.
  - Die Rekonstruktion läuft für Fog-Instanzen deshalb in der Cloud, mit den Korrelationen vom Gateway (DBG-15) und der Zuordnung lokaler IDs zu Cloud-IDs aus dem Deployment.
  - Lücken entstehen, solange das Gateway offline ist: Der Connector puffert nur im Speicher, bis 50 000 Nachrichten, und sendet mit QoS 0. Die Ansicht kennzeichnet solche Zeiträume.
  - Auf dem Gateway selbst gibt es keine Zeitreihe, nur mgw-last-value. Ohne Cloud-Verbindung ist deshalb nur der aktuelle Wert auswertbar (DBG-18).

---

## 11. Historie und Aufbewahrung

- **HIST-01 (M)** Historie auf Instanzebene: Instanz-ID, Laufnummer, Deployment und Deployment-Version des Laufs, Modell und Modellversion (sofern vorhanden), Name, Start, Ende, Endzustand (beendet, extern beendet, Incident) und Startvariablen. Das nutzen web-ui, canary, Widgets und Smart Services.
- **HIST-02 (M)** Historienabfrage im fachlichen Umfang des heutigen Monitors:
  - beendet/laufend;
  - Namespace, Deployment-ID, Deployment-Name (Teilstring), Modell-ID, Instanz-ID;
  - Sortierung, Paging, Gesamtzahl.

  Die heutigen Parameternamen (`processDefinitionId`, `processDefinitionNameLike`, `processInstanceBusinessKey`, `maxResults`, `firstResult`, `with_total`) gibt es nur unter `deprecated/`.
- **HIST-03 (M)** Historie löschen, einzeln und mehrere, mit Kaskade auf IO, Incidents und Debug-Verlauf.
- **HIST-04 (M)** Die Aufbewahrung wird über einen Wert und Obergrenzen geregelt (entschieden 2026-10-12, ersetzt „der längste Wert gilt“). Ziel ist, dass niemand sehr lange Fristen einstellt und so große Datenmengen erzeugt.
  - **Obergrenzen** setzen Admins: global, je Namespace (NS-07) und je Nutzer.
  - **Vorgabe:** Sie gilt, wenn am Deployment nichts eingestellt ist; global 7 Tage, je Namespace überschreibbar.
  - **Wert am Deployment:** Den stellt der Owner ein, innerhalb der Obergrenzen. Ein höherer Wert wird beim Speichern abgelehnt.
  - **Wirksam** ist der kleinste der Werte: Deployment-Wert bzw. Vorgabe, Obergrenze global/Namespace, Obergrenze des Owners der Instanz (SEC-12). Beispiele:
    - Nutzer-Grenze 7 Tage, Deployment 1 Tag ergibt 1 Tag.
    - Nutzer-Grenze 1 Tag, Deployment 7 Tage ergibt 1 Tag.
    - Keine Angaben, Vorgabe 7 Tage ergibt 7 Tage.
  - Wird eine Obergrenze gesenkt, gilt sie sofort, auch für bestehende Daten.
  - Das gilt für Instanzhistorie, Aktivitätsverlauf, Incidents und Debug-Verlauf (Abschnitt 13), denn es sind dieselben Daten.
  - Die Engine setzt die Aufbewahrung selbst durch; CronJob und Batch-Fenster entfallen.
  - Grenzen gelten auch bei langer Aufbewahrung, siehe LONG-03 und LONG-07.
- **HIST-05 (M)** Aktivitätsverlauf je Instanz, für laufende und beendete Instanzen:
  - welches Element wann betreten und verlassen wurde;
  - gewählter Ausgang an Gateways und ausgewertete Bedingungen;
  - Variablenänderungen mit Element;
  - Kommandos und Antworten;
  - Korrelationen und Timer;
  - Skriptausgaben, Incidents.

  Er ist die Grundlage für das historische Debugging (DBG-13).
- **HIST-06 (M)** Verlaufsdaten laufender Instanzen haben eine Aufbewahrung, die nicht vom Instanzende abhängt (Abschnitt 12). Die Historie reicht in Prod trotz 7 Tagen TTL etwa 8 Monate zurück, wahrscheinlich wegen nie endender Instanzen.

---

## 12. Langlebige Prozesse und begrenzter Speicher

Prozesse, die monate- oder jahrelang laufen, sind ein Haupteinsatzfall: Ein Prozess wartet auf Sensordaten, schaltet etwas und kehrt zum Warten zurück. Beispiele sind der Lüfterprozess aus den Vault-Notizen „A process incident ends the instance unless restart is set“ und „Comparing two sensors inside a process“. Solche Prozesse müssen ohne Eingriff laufen, ohne dass die Datenbank mit Laufzeit oder Anzahl der Schleifendurchläufe wächst. Auf Gateways mit begrenztem Speicher funktioniert das heute nicht.

### Warum es heute wächst

- **Camunda räumt Historie nur für beendete Instanzen auf.** Die Löschzeit (`REMOVAL_TIME_`) wird erst bei Instanzende gesetzt. Jeder Schleifendurchlauf schreibt je durchlaufenem Element eine Zeile in `ACT_HI_ACTINST`, und für eine nie endende Instanz löscht nichts davon (Vault: „History cleanup never runs without a batch window“, Abschnitt „Not this if“).
- **Das Gateway hat dieselbe Engine-Konfiguration.** Der Cleanup im Sync-Client (alle 24 h, Höchstalter 168 h) löscht ebenfalls nur beendete Instanzen.
- **Postgres gibt gelöschten Platz nicht an das Dateisystem zurück.** Erst `VACUUM FULL` lässt die Datei schrumpfen.
- **Parallele Zweige ohne Zusammenführung** sind in den Exporten häufig (20 verzweigende, 5 zusammenführende parallele Gateways). Führt ein solcher Zweig in einer Schleife zurück vor die Verzweigung, vervielfacht sich die Zahl der Token.

### Anforderungen

- **LONG-01 (M)** Der Speicherbedarf einer laufenden Instanz ist begrenzt. Er hängt von der Zahl aktiver Elemente und Variablen ab, nicht von Laufzeit oder Zahl der Durchläufe.
- **LONG-02 (M)** Laufzeitzustand wird überschrieben, nicht angehängt. Abgeschlossene Tasks, ausgelöste Timer, korrelierte Subscriptions und erledigte Teilaufgaben werden beim Abschluss gelöscht.
- **LONG-03 (M)** Verlaufsdaten laufender Instanzen (HIST-05, Incidents) werden unabhängig vom Instanzende aufgeräumt:
  - nach Alter gemäß HIST-04;
  - zusätzlich nach Anzahl je Instanz als Ringpuffer, mit konfigurierbarer Obergrenze; die Vorgabewerte werden bei der Implementierung festgelegt.

  Die Obergrenze verhindert, dass eine lange Aufbewahrung bei schnellen Schleifen unbegrenzt Daten ansammelt. Wird sie erreicht, sieht der Nutzer das im Debugging („ältere Einträge verdrängt“).
- **LONG-04 (M)** Zähler statt Einzelzeilen, wo nur eine Anzahl gebraucht wird: Durchläufe je Element, letzte Ausführung, letzter Fehler.
- **LONG-05 (M)** Das Aufräumen läuft in der Engine laufend und in kleinen Schritten, ohne Batch-Fenster und ohne CronJob.
- **LONG-06 (M)** Gelöschter Platz wird wieder frei; die Datei wächst nach einer Einschwingphase nicht weiter. Bei SQLite z. B. mit `auto_vacuum=incremental` und regelmäßigem `incremental_vacuum`.
- **LONG-07 (M)** Speicherbudget auf dem Gateway konfigurierbar; die Vorgabe wird bei der Implementierung festgelegt.
  - Nähert sich die Belegung dem Budget, geht eine Warnung an Cloud und Nutzer, und Verlaufsdaten werden über die eingestellte Aufbewahrung hinaus gekürzt, älteste zuerst.
  - Auf dem Gateway hat das Budget also Vorrang vor HIST-04.
  - Laufzeitzustand hat Vorrang vor Verlauf.
  - Ist der Datenträger voll, geht die Engine in einen definierten Fehlerzustand und hinterlässt keinen beschädigten Zustand.
- **LONG-08 (M)** Größenlimits je Variable und je Instanz. Eine Überschreitung erzeugt einen Incident. Typischer Fall: ein Skript, das in jedem Durchlauf an eine Liste anhängt.
- **LONG-09 (M)** Die Synchronisation sendet nicht einen Eintrag je Schleifendurchlauf nach oben. Nach oben gehen:
  - Änderungen auf Instanzebene;
  - Schrittdetails nur, solange jemand zuschaut (DBG-12) oder wenn sie für das historische Debugging in der Cloud abgerufen werden (DBG-15).

  Heute meldet der Sync-Client jede Änderung an `ACT_RU_EXECUTION` per Trigger nach oben.
- **LONG-10 (M)** Der Offline-Puffer der Synchronisation ist begrenzt. Er wird je Instanz auf den letzten Stand zusammengefasst; über einer Obergrenze geht nach der Wiederverbindung ein Snapshot.
- **LONG-11 (M)** Nebenablagen sind begrenzt:
  - IO-Werte ohne Instanzbindung je Nutzer bzw. je Gateway, nach Anzahl und Größe;
  - Startparameter werden mit Instanz bzw. Deployment gelöscht;
  - auf der Cloud-Seite der Synchronisation gilt dieselbe Aufbewahrung wie in der Engine.
- **LONG-12 (M)** Laufende Instanzen überstehen Neustarts, Updates der Engine und Schemaänderungen. Für Timer nach einer Ausfallzeit gibt es eine Nachholregel: verpasste Zyklen höchstens einmal ausführen, nicht alle nachholen.
- **LONG-13 (S)** Instanzen ohne Fortschritt sind erkennbar: Metrik und Anzeige „seit Zeitraum X ohne Elementwechsel“.
- **LONG-14 (M)** Metriken zum Speicher:
  - Größe nach Laufzeitzustand, Verlauf, Outbox und IO;
  - die Instanzen mit den meisten Verlaufseinträgen.

  Im Fog gehen diese Werte an die Cloud.
- **LONG-15 (M)** Abnahme durch einen Dauertest auf einem arm64-Gateway.
  - Getestet werden repräsentative Schleifenprozesse aus den exportierten Modellen, darunter parallele und inklusive Gateways, Gerätekommandos, Timer-Zyklen und IO.
  - Die Ereignisrate ist hoch, damit viele Monate Betrieb in kurzer Zeit durchlaufen werden; dazu kommen Offline-Phasen und Neustarts.
  - Kriterium: Datenbankgröße und Speicherverbrauch bleiben nach der Einschwingphase stabil.
- **LONG-16 (M)** Höchstzahl gleichzeitiger Token je Instanz, konfigurierbar. Eine Überschreitung erzeugt einen Incident statt unbegrenzten Wachstums (siehe parallele Zweige oben).

---

## 13. Debugging

Heute gibt es dafür nichts. Cockpit ist entfernt, der Wrapper liest keine Variablen, und die Monitor-Detailansicht zeigt nur das Diagramm ohne Zustand. Entschieden ist:
- markiert wird im Client;
- Schritt für Schritt auch auf historischen Daten;
- so detailliert wie möglich;
- Event-Auswertungen werden implizit aus den Timescale-Daten rekonstruiert und nicht vom event-worker gemeldet (EVT-09 bis EVT-13).

Live- und Historiendaten haben dieselbe Form: Der Live-Strom ist das Ende des Aktivitätsverlaufs (HIST-05).

### Live-Endpunkt

- **DBG-01 (M)** WebSocket-Endpunkt je Instanz, z. B. `GET /v2/process-instances/{id}/live`.
  - Zuerst kommt ein Snapshot, danach Deltas mit fortlaufender Sequenznummer.
  - Nach einem Verbindungsabbruch setzt der Client ab einer Sequenz fort; ist die Lücke zu groß, kommt ein neuer Snapshot.
  - Endet die Instanz, kommt der Endzustand, danach wird die Verbindung geschlossen.
- **DBG-02 (M)** Der Snapshot enthält:
  - **Instanz**: ID, Laufnummer und frühere Läufe, Deployment und Version, Zustand, Startzeit, Startvariablen.
  - **Aktive Elemente**: BPMN-ID, Typ, Name, aktiv seit. Mehrere sind möglich, durch parallele Zweige und ereignisbasierte Gateways.
  - **Wartegrund** je Element:
    - Gerätekommando: Zielgerät und Service, Strategie, Teilaufgaben mit Status, Versuch n von `retries`, Timeout.
    - Message-Event: `event_id`, Quelle, Bedingungsskript, aktueller Wert der Quelle mit Auswertung (DBG-18), auf Anforderung die rekonstruierten Auswertungen seit Beginn des Wartens (EVT-10).
    - Timer: Definition und nächste Fälligkeit.
    - Script Task: läuft seit.
    - Smart-Service-Modul: Modultyp und Fortschritt.
  - **Variablen**: Instanz- und lokale Variablen, je mit Typ, Wert, letzter Änderung und änderndem Element.
  - **Offene Incidents**.
- **DBG-03 (M)** Deltas für alle Einträge aus HIST-05.
- **DBG-04 (M)** Große Werte werden gekürzt übertragen und lassen sich einzeln über HTTP nachladen.

### Historisches Debugging

- **DBG-13 (M)** Beendete und laufende Instanzen lassen sich innerhalb der Aufbewahrung Schritt für Schritt nachvollziehen:
  - vorwärts und rückwärts schrittweise;
  - Sprung zu einem Zeitpunkt oder Eintrag;
  - je Schritt der Zustand wie im Snapshot (aktive Elemente, Variablen, Wartegrund).
- **DBG-14 (M)** Dafür werden Variablenstände so gespeichert, dass sich jeder Schritt rekonstruieren lässt: Änderungen als Deltas plus periodische Vollstände. Die Grenzen aus LONG-03 gelten. Ist der Anfang verdrängt, beginnt der Rückblick beim ältesten verbliebenen Vollstand.
- **DBG-15 (M)** Fog-Instanzen lassen sich historisch debuggen:
  - Der Verlauf bleibt auf dem Gateway.
  - Die Cloud ruft ihn bei Bedarf ab, in Abschnitten über die Synchronisation.
  - Ist das Gateway offline, steht nur der Stand zur Verfügung, den die Cloud schon hat; er ist als solcher gekennzeichnet.
  - Event-Auswertungen werden auch für Fog-Instanzen aus den Timescale-Daten in der Cloud rekonstruiert (EVT-13).
- **DBG-16 (S)** Suche im Verlauf, z. B. „wann hat Variable X den Wert Y bekommen“, „wann hat Event Z ausgelöst“, „alle Incidents“.
- **DBG-18 (M)** Für ein wartendes Event zeigt die Live-Ansicht den aktuellen Wert der Quelle und ob die Bedingung damit jetzt erfüllt wäre.
  - Die Quelle ist der letzte Wert über timescale-wrapper, im Fog mgw-last-value.
  - Abgefragt wird beim Öffnen und, solange jemand zuschaut, in einem gedrosselten Intervall; ohne Zuschauer entstehen keine Kosten.

### Diagramm

- **DBG-05 (M)** Der Client markiert das beim Deploy gespeicherte SVG der passenden Deployment-Version.
  - bpmn-js schreibt beim Export für jedes Element `data-element-id`; geprüft an `smart-service-repository/pkg/tests/resources/process_deployment.svg` und den Fixtures in process-deployment.
  - Die Engine liefert das SVG einmal und danach nur Element-IDs.
- **DBG-06 (M)** Unterschiedene Markierungen: aktiv, durchlaufen (mit Anzahl, LONG-04), Incident, wartend auf Event mit Auswertung des aktuellen Werts (DBG-18).
- **DBG-07 (S)** Durchlaufene Sequenzflüsse werden markiert, damit der genommene Weg durch Gateways sichtbar ist.

### Zugriff, Last, Betrieb

- **DBG-08 (M)** Zugriff mit Leserecht auf die Instanz (NS-05) oder als Admin.
  - Das Token geht beim WebSocket als Subprotokoll oder als erste Nachricht, nicht als Query-Parameter, sonst landet es in Zugriffslogs.
  - Kong muss das Upgrade durchreichen, die Ladon-Regel muss den Pfad abdecken.
  - Läuft das Token ab, wird die Verbindung geschlossen oder ein neues angefordert.
- **DBG-09 (M)** Bei mehreren Replicas kommen die Deltas an, ohne dass Sticky Sessions vorausgesetzt werden; Quelle ist der Änderungsstrom (SYNC-03).
- **DBG-10 (M)** Begrenzungen:
  - Verbindungen je Nutzer;
  - Drosselung bei schnellen Schleifen: Deltas zusammenfassen, Variablen als letzter Stand;
  - ein langsamer Client bekommt einen neuen Snapshot statt verpasster Deltas.
- **DBG-11 (M)** Skriptausgaben aus `log(...)` (EXPR-04) erscheinen im Verlauf am Element, das sie erzeugt hat.
- **DBG-12 (S)** Live-Debugging von Fog-Instanzen über denselben Endpunkt mit `networkId`. Der Live-Strom vom Gateway läuft nur, solange jemand zuschaut.
- **DBG-17 (K)** Eingriffe aus der Debug-Ansicht, getrennt vom Lesestrom und eigens berechtigt: Variable setzen (RUN-05), Event manuell auslösen, Timer sofort fällig stellen.

---

## 14. Mandanten, Authentifizierung, Berechtigungen

- **SEC-01 (M)** Abfragen und Aktionen werden nach den Rechten in permissions-v2 gefiltert (NS-05, SEC-07), also eigene und geteilte Ressourcen. Wem eine Ressource gehört, steht im Owner-Feld der Engine (SEC-09). Heute filtert der Wrapper nur nach Mandant (`tenantIdIn`).
- **SEC-02 (M)** Eine Admin-Rolle sieht und handelt mandantenübergreifend, für Housekeeping und interne Dienste. Heute geht das nur über die Camunda-REST-API mit Basic Auth.
- **SEC-03 (M)** Interne Aufrufe laufen mit Client-Credentials oder Token Exchange und gültigen Tokens. Heute ruft der Wrapper die incident-api mit einem hart codierten, abgelaufenen Admin-Token auf.
- **SEC-04 (M)** JWT-Behandlung nach Plattformstandard (Kong plus Ladon). Heute nutzt der Wrapper `ParseUnverified`.
- **SEC-05 (M)** Gateway-Berechtigung über permissions-v2, Topic `hubs`: lesen und starten mit `rx`, deployen und löschen mit `a`.
- **SEC-06 (M)** Jede Instanz läuft mit den Rechten ihres Owners, unabhängig vom Namespace: Token Exchange für Gerätekommandos (TASK-02) und für Module (TASK-28). Das Teilen regelt der Namespace (NS-05). Smart-Service-Instanzen laufen heute in einer eigenen Engine ohne Mandanten.
- **SEC-07 (M)** Modelle, Deployments und Instanzen sind Ressourcen in permissions-v2 (NS-05). Was beim Anlegen geteilt ist, bestimmen die Default-Permissions des Namespace; danach können Nutzer mit Administrieren weitere Rechte vergeben. Jeder Zugriff wird gegen permissions-v2 geprüft, nicht nur gegen den Mandanten.
- **SEC-08 (M)** Beim Löschen eines Nutzers wird alles entfernt, dessen Owner er ist: Modelle, Deployments und Instanzen in allen Namespaces samt Modulen, Historie, Debug-Verlauf, IO-Daten, Incidents und Zeitpläne. Rechte, die ihm an fremden Ressourcen geteilt wurden, werden entfernt. Heute bleiben Reste stehen (Vault: „Deleting a user leaves process models and releases behind“).

  Was mit fremden Abhängigen geschieht, regelt SEC-13.
- **SEC-13 (M)** Fremde Abhängige beim Löschen eines Nutzers sind Ressourcen anderer Owner, die an seinen Ressourcen hängen:
  - Instanzen anderer Nutzer auf seinen geteilten Deployments, z. B. Smart-Service-Instanzen auf seinem Release;
  - Deployments anderer Nutzer, die eine seiner Instanzen über ein Modul verwaltet (NS-08).

  Möglich sind zwei Verhalten; eine Konfiguration entscheidet (entschieden):
  - **Löschkaskade**: Die fremden Abhängigen werden mitgelöscht. Ihre Owner bekommen eine Benachrichtigung mit dem Grund.
  - **Fehlermeldung**: Das Löschen wird abgelehnt, mit einer Liste der fremden Abhängigen (Ressource, Namespace, Owner). Der Admin kann vorher Owner wechseln (SEC-11) oder die Abhängigen entfernen.

  Vorschlag: Die Konfiguration gilt je Namespace, mit einer Plattformvorgabe, so wie die übrigen Namespace-Einstellungen (NS-07). Fremde Deployments auf gelöschten Modellen sind keine Abhängigen (MOD-08).
- **SEC-14 (M)** Das Löschen eines Nutzers läuft über einen synchronen Admin-Aufruf der Engine, z. B. `DELETE /users/{id}`. Dazu kommt eine Vorabprüfung ohne Wirkung, z. B. `GET /users/{id}/dependents`.
  - user-management ruft heute die Dienste der Reihe nach synchron auf (`pkg/ctrl/user.go` `DeleteUser`). Bei einem Fehler bricht es ab, Keycloak kommt zuletzt.
  - Die Fehlermeldung aus SEC-13 hält das Löschen also vor Keycloak an. Dienste, die vorher aufgerufen wurden, haben ihre Daten aber schon gelöscht, die device-repository als erster.
  - user-management ruft deshalb die Vorabprüfung **vor** allen Löschaufrufen auf und bricht mit der Meldung ab, wenn die Engine ablehnen würde.
  - Das Löschen über Kafka `user` (heute process-deployment, process-model-repository) bleibt nur als Absicherung für Nutzer, die außerhalb von user-management gelöscht werden.

### Owner

Bei Geräten und Hubs gab es Probleme, solange die Zuordnung zum Nutzer nur in permissions-v2 stand. Dort wurde deshalb ein Owner eingeführt (`device-repository`: `Device.OwnerId`, `Hub.OwnerId`, `Graph.Owner`). Dasselbe gilt hier (entschieden).

- **SEC-09 (M)** Jedes Modell, jedes Deployment und jede Instanz hat ein Owner-Feld in der Engine. Es wird nicht aus permissions-v2 abgeleitet. Der Owner ist die Grundlage für:
  - die Unterscheidung von eigenen und geteilten Ressourcen in Listen;
  - das Löschen beim Löschen eines Nutzers (SEC-08);
  - die Rechte, mit denen eine Instanz arbeitet (SEC-06);
  - die Kostenzuordnung: `user_id` im Metrik-Label ist der Owner der Instanz (TASK-12);
  - Grenzen je Nutzer (LONG-11);
  - die Synchronisation mit Gateways.
- **SEC-10 (M)** Der Owner hat **zwingend** Administrieren in permissions-v2 (entschieden), wie bei Geräten.
  - Beim Anlegen setzt die Engine die Default-Permissions des Namespace und gibt dem Owner Administrieren. Vorbild ist `device-repository/lib/controller/security.go` `EnsureInitialRights`.
  - Wer dem Owner das Recht entziehen will, wird abgewiesen. Ist das nicht möglich, weil die Änderung direkt in permissions-v2 passiert, stellt die Engine das Recht sofort wieder her.
  - Laut Code prüft device-repository das Admin-Recht des Owners nur beim Anlegen und beim Owner-Wechsel. permissions-v2 selbst kennt keinen Owner. Ein direkter Entzug über permissions-v2 ist dort also nicht abgefangen (Abschnitt 22).
- **SEC-11 (M)** Owner-Wechsel nach dem Muster von device-repository (`hub.go`, `device.go`):
  - Erlaubt ist er nur Nutzern mit Administrieren an der Ressource oder Plattform-Admins.
  - Der neue Owner muss bereits Administrieren haben.
  - Ohne Angabe ist der Owner der anfragende Nutzer; nur Admins dürfen beim Anlegen einen anderen Owner setzen.
- **SEC-12 (M)** Wer Owner einer Instanz wird:
  - beim Start durch einen Nutzer der startende Nutzer, auch auf einem geteilten Deployment;
  - bei Starts ohne Nutzer (Zeitplan, Timer-Startereignis, Message-Start durch ein Gerät) der Owner des Deployments;
  - bei Instanzen, die ein Modul anlegt (NS-08), der Owner der verwaltenden Instanz.

  Ein neuer Lauf (RUN-11) behält den Owner der Instanz. Ein Owner-Wechsel am Deployment ändert den Owner bestehender Instanzen nicht.

---

## 15. API

- **API-01 (M)** Eine neue, einheitliche API ohne Altlasten (entschieden).
  - **Ressourcen:** Namespaces, Modelle (mit Versionen), Deployments (mit Versionen), Instanzen (mit Läufen), Verlauf, Incidents, IO-Werte, Zeitpläne, Module, Live- und Debug-Zugriff.
  - **Begriffe** nach dem Datenmodell (Abschnitt 5): `deployment_id`, `instance_id`, `run`, `namespace`. Es gibt keine Business Keys, keine Prozessdefinitionen und keine vid.
  - **Eine Basis-URL** statt der heutigen zehn Routen von Wrapper, process-deployment, process-model-repository, process-io, Incidents, Scheduler, smart-service-repository, process-sync und process-fog-deployment.
  - **Namespace in jeder Ressource:** Er steht im Pfad, z. B. `/namespaces/{ns}/deployments`, oder ist Pflichtfilter.
- **API-02 (M)** Alte APIs und alle Aliase sind **nur unter einem Pfad `deprecated/`** erreichbar (entschieden), z. B. `.../deprecated/engine/v2/...`, `.../deprecated/deployment/v3/...`. Darunter fallen:
  - die Wrapper-Routen v2, darunter `/v2/process-definitions/...`, Start per `GET` mit `business_key` und Löschen per Business Key;
  - process-deployment `/v3/...`;
  - process-model-repository `/processes` und `/v2/processes`;
  - process-io, incident-api und process-scheduler;
  - smart-service-repository, darunter `/designs`, `/releases`, `/extended-releases`, `/instances`, `/modules` und `/instances-by-process-id/...`;
  - process-sync und process-fog-deployment;
  - die alten Parameter- und Feldnamen (`processDefinitionId`, `process_definition_id`, `processInstanceBusinessKey`, `maxResults`/`firstResult`).
- **API-03 (M)** Die Schicht unter `deprecated/` übersetzt nur auf die neue API. Sie hat keine eigene Logik und keinen eigenen Speicher, damit sie ohne Rückwirkung entfernt werden kann. Jeder Aufruf wird je Route gezählt (Metrik); daran ist ablesbar, wann sie abgeschaltet werden kann.
- **API-04 (M)** Die eigenen Clients werden auf die neue API umgestellt: web-ui (Monitor, Deployments, Modelle, Smart Services, Fog-Ansichten, Widgets), canary und snowflake-canary, user-management. Nach der Umstellung nutzt kein Plattformdienst mehr `deprecated/`.
- **API-05 (M)** Cloud und Fog haben eine API: dieselben Ressourcen mit optionalem `networkId`.
- **API-06 (M)** REST-Semantik:
  - Starten mit `POST`, bzw. `PUT` bei vorgegebener ID (RUN-01); kein zustandsändernder `GET`;
  - Paging einheitlich;
  - Berechtigungsfehler mit 403, nicht 401.
- **API-07 (M)** OpenAPI nach Plattformstandard. Die `deprecated/`-Routen sind darin als veraltet markiert.
- **API-08 (M)** Kong leitet die heutigen Pfade auf `deprecated/` um (entschieden). Beispiel: `/process/engine/v2/deployments/{id}/start` wird zu `.../deprecated/engine/v2/deployments/{id}/start`.
  - So funktionieren auch Start-URLs, die Nutzer über „Start-URL kopieren“ außerhalb der Plattform eingebaut haben.
  - Die Umleitung ist reine Kong-Konfiguration (Pfad-Umschreibung). Die Engine kennt die alten Pfade nicht.
  - Ladon-Regeln für die alten Pfade werden mit umgestellt.
  - Die Umleitung fällt mit der `deprecated/`-Schicht weg, wenn deren Metrik (API-03) keine Aufrufe mehr zeigt.

### Go-Client-Bibliothek

Heute bringt jeder Dienst einen eigenen, handgeschriebenen Go-Client mit, z. B. `camunda-engine-wrapper/lib/client`, `process-incident-api/lib/client`, `event-deployment/lib/client` und `permissions-v2/pkg/client`. Felder, die eine Seite nicht kennt, gehen beim Dekodieren ohne Fehler verloren. Das ist zweimal dokumentiert:
- Vault: „Moving a type into the shared model has a publish order“;
- Vault: „A consumer that copied the type is invisible to the sibling build“; ein Beispiel ist der Round-Trip im Smart-Service-Worker, der `aspect_ids` verlor.

Eine Client-Bibliothek gibt es nur für Go. Für JavaScript/TypeScript gibt es keine (entschieden): Die Frontends setzen die API selbst um, auf Basis der OpenAPI (API-07). Das web-ui wird dafür angepasst (API-04). Der Client deckt nur die neue API ab, nicht `deprecated/`.

- **CLI-01 (M)** Eine Quelle für die Typen. Go-Typen und OpenAPI werden nicht unabhängig voneinander gepflegt: Entweder sind die Go-Typen des Servers die Quelle und OpenAPI wird daraus erzeugt, oder umgekehrt. Die Erzeugung läuft in CI. Weicht der Client vom Server ab, scheitert der Build. Die Frontends nutzen dieselbe OpenAPI.
- **CLI-02 (M)** Der Client deckt die gesamte neue API ab:
  - Namespaces, Modelle, Deployments, Instanzen und Läufe;
  - Verlauf, Incidents, IO-Werte, Zeitpläne, Module;
  - Live- und historisches Debugging;
  - Abfragen mit `networkId` für Gateways.
- **CLI-03 (M)** Fehler kommen typisiert an. Mindestens unterschieden werden: nicht gefunden, keine Berechtigung, Konflikt (z. B. idempotenter Start mit abweichendem Inhalt, RUN-01), Validierungsfehler mit Element-Bezug (BPMN-18) und nicht erreichbar.
- **CLI-04 (M)** Hilfen für Paging: Iteratoren über alle Seiten.
- **CLI-05 (M)** Ein Client für den Debug-Strom (DBG-01):
  - nach einem Abbruch ab der letzten Sequenz fortsetzen;
  - Snapshot und Deltas zu einem aktuellen Zustandsobjekt zusammenführen.

  Für das historische Debugging bietet er dieselbe Zustandsform mit Schritt vor, Schritt zurück und Sprung (DBG-13).
- **CLI-06 (M)** Versionierung und Veröffentlichung:
  - Der Client wird mit der API versioniert und gemeinsam mit dem Server veröffentlicht, über die Release-Pipeline der Plattform.
  - Unter Apache-2.0 wie die übrigen SENERGY-Repos.
  - Ein inkompatibler API-Bruch braucht im Go-Modul einen neuen Major-Pfad (`/vN`). Sonst vergibt die Pipeline einen Major-Tag, den Go als ungültige Version ablehnt (`senergy-wissen/03 Werkzeuge/CI/Die Version vergibt die Pipeline.md`).
- **CLI-07 (M)** Der Client wird gegen eine laufende Engine geprüft, mit dem Test-Client aus CLI-09.
- **CLI-08 (M)** Go-Client im Repo der Engine unter `pkg/client`, nach Plattformkonvention (wie `permissions-v2/pkg/client`, `device-selection/pkg/client`):
  - ein Interface `Client` mit Konstruktor `New(url)`;
  - Typen aus dem Paket des Servers, keine Kopien.
- **CLI-09 (M)** Ein Test-Client, z. B. `NewTestClient(ctx)`, startet die Engine im Prozess mit eingebetteter Datenbank (SQLite) und erfüllt dasselbe Interface. Konsumenten testen damit ohne Netzwerk und ohne Container. Vorbild ist `permissions-v2/pkg/client.NewTestClient`.
- **CLI-10 (M)** Authentifizierung:
  - Token je Aufruf (Nutzer-Token);
  - Token-Provider für Client-Credentials und Token Exchange, wie `permissions-v2/pkg/client.NewTokenProvider`.
- **CLI-11 (S)** Die Bibliotheksteile, die heute andere Repos aus `external-task-worker/lib` importieren (Marshaller, Messages; device-command, live-data-worker), bekommen einen stabilen Ort. Sie gehören nicht in den API-Client.

## 16. Fog: Engine auf dem Gateway

- **FOG-01 (M)** Dasselbe Engine-Binary läuft in Cloud und Fog. Die Fog-Betriebsart nutzt:
  - MQTT statt Kafka;
  - lokale Geräte-IDs;
  - den Marshaller im Prozess;
  - den device-repository-Mirror;
  - mgw-last-value.
- **FOG-02 (M)** Images für amd64 und arm64. arm/v7 entfällt; alte Gateway-Versionen sind irrelevant (entschieden).
- **FOG-03 (M)** Eingebettete Persistenz auf dem Gateway. Sie ersetzt zwei Postgres-11.2-Container (Engine, process-io) und die bolt-DB des Sync-Clients.
  - Empfehlung: SQLite, weil die Cloud Postgres nutzt und sich Schema und Abfragen weitgehend teilen lassen.
- **FOG-04 (M)** Eine Zielhardware ist nicht vorgegeben. Das Speicherbudget ist konfigurierbar (LONG-07), der Dauertest läuft auf einem arm64-Gateway (LONG-15).
- **FOG-05 (M)** Volle Funktion ohne Cloud-Verbindung: Instanzen, Timer, Events, Incidents mit Restart, Historie, Debug-Verlauf.
- **FOG-06 (M)** Lokale Eventauswertung auf MQTT `event/{localDeviceId}/{localServiceId}`, mit den Eventbeschreibungen aus dem Deployment. Der event-worker im Fog holt sie von der Engine; heute fragt er die HTTP-API des Sync-Clients ab (`GET /event-descriptions`).
- **FOG-07 (M)** Incident-Behandlung mit persistentem Restart-Zähler, auf **einer** Ebene. Heute gibt es zwei Ebenen, Gateway und Cloud-Reconciler, die sich in die Quere kommen.
- **FOG-08 (M)** Benachrichtigungen gehen über den cloud-proxy.
- **FOG-09 (M)** Aufgabentyp „HTTP-Aufruf“ ins lokale Netz (BPMN-10).
- **FOG-10 (S)** Die Engine bringt ihre Schemaänderungen selbst mit. Heute migrieren Wrapper und Sync-Client die Camunda-Tabellen von außen (Vault: „Camunda does not apply its own schema updates“).

---

## 17. Synchronisation Cloud–Gateway (bisher process-sync und mgw-process-sync-client)

process-sync (Cloud) und mgw-process-sync-client (Gateway) werden in die Engine integriert (entschieden). Beide Seiten laufen mit derselben Engine, demselben Datenmodell, denselben IDs und denselben Versionen. Die Synchronisation ist damit Replikation zwischen zwei gleichen Engines.

Heute hängen beide Dienste an Camunda-Interna: Postgres-Trigger auf `ACT_RU_EXECUTION` und weiteren Tabellen, Schema-Migrationen von außen, Zuordnung der Camunda-Deployment-IDs. Was der Sync-Client darüber hinaus leistet, ist in diesem Dokument bereits Engine-Funktion:
- Deploy (DEP-09);
- Eventbeschreibungen für den event-worker (FOG-06);
- Restart-Logik (FOG-07);
- History-Cleanup (Abschnitt 12);
- Metadaten in bolt (FOG-03).


- **SYNC-01 (M)** Nach unten wird ein Soll-Zustand repliziert, idempotent und mit der Cloud-UUID als ID:
  - Deployments mit Version und gewählter Update-Strategie (DEP-04);
  - Löschungen;
  - Starts mit vorgegebener Instanz-ID und Parametern, idempotent (RUN-01).
- **SYNC-02 (M)** Nach oben wird ein Änderungsstrom auf Instanzebene repliziert, mit Sequenznummern und Bestätigung:
  - Deployments und Versionen;
  - Instanzen und Historie;
  - Incidents und Fehler.

  Nach einer Offline-Phase holt er ab der letzten bestätigten Position nach. Er ersetzt die Postgres-Trigger, die `known`-Listen alle 3 h und das Senden mit QoS 0 und Puffer nur im Speicher.
- **SYNC-03 (M)** Die Engine stellt den Änderungsstrom als Outbox mit Sequenz bereit. Ihn nutzen die Synchronisation, der Live-Endpunkt (DBG-09) und der event-worker.
- **SYNC-04 (M)** Der Transport läuft weiter über MQTT via mgw-device-cloud-connector, Schema `processes/{networkId}/...`, ergänzt um:
  - Abruf von Verlaufsabschnitten (DBG-15);
  - Live-Strom auf Anforderung (DBG-12).
- **SYNC-05 (M)** Die Cloud-Seite liefert eine Lese- und Kommando-Fassade je `networkId` als Teil der Engine-API (API-05), mit permissions-v2 `hubs`.
- **SYNC-06 (M)** Eventbeschreibungen für Gateways werden in der Cloud erzeugt und bei Änderungen an Gerätegruppen nachgeliefert.
- **SYNC-07 (S)** Daten eines Netzes, das länger als `cleanup_max_age` (heute 730 h) keinen Kontakt hatte, werden entfernt.
- **SYNC-08 (M)** Die Ideen aus dem nicht gemergten Branch `feat/platform-source-of-truth` in process-sync wandern in die Engine: Soll-Zustand je Deployment, Reconciler, erneutes Senden nach Frist. Fingerprints und Duplikatbereinigung entfallen, weil IDs stabil und Deploys idempotent sind (DEP-02, RUN-01). Der Branch selbst wird nicht mehr gemergt.
- **SYNC-09 (M)** Keine eigenen Dienste mehr:
  - **Gateway:** Die Synchronisation ist Teil des Engine-Prozesses, ohne eigenen Container und ohne eigene Datenbank. Die bolt-DB des Sync-Clients entfällt.
  - **Cloud:** Die Synchronisation ist Teil der Codebasis der Engine. Sie läuft entweder in den Engine-Replicas oder als eigene Rolle „sync“ desselben Binaries, analog zum event-worker (Abschnitt 10). Fog-Daten liegen in der Engine-Datenbank als Ressourcen mit `networkId`. Rechte, Historie, Debugging und Namespaces gelten für sie ohne eigene Implementierung; die Mongo von process-sync entfällt.
- **SYNC-10 (M)** MQTT bei mehreren Replicas. Heute hat process-sync eine Replica und eine persistente MQTT-Session (`MQTT_CLEAN_SESSION=false`). Künftig:
  - Jede Nachricht von einem Gateway wird genau einmal verarbeitet, entweder über geteilte Subscriptions im Broker (VerneMQ: `$share/<gruppe>/...`) oder über eine Leader-Wahl für den Sync-Teil.
  - Eine Nachricht wird erst bestätigt, wenn ihre Wirkung in der Datenbank festgeschrieben ist. Der Neustart einer Replica verliert keine Nachrichten.
  - Welcher Weg genommen wird, steht vor der Umsetzung fest. Eine eigene Rolle „sync“ mit einer Replica und Leader-Wahl ist der einfachste Weg; geteilte Subscriptions erlauben mehrere aktive Replicas.
- **SYNC-11 (M)** Das Sync-Protokoll ist versioniert.
  - Gateways werden später aktualisiert als die Cloud, Cloud und Gateway laufen also zeitweise mit verschiedenen Engine-Versionen.
  - Jede Nachricht trägt eine Protokollversion. Die Cloud unterstützt die aktuelle und mindestens die vorige Gateway-Version, das Fenster ist konfigurierbar.
  - Ein Gateway außerhalb des Fensters wird als inkompatibel gekennzeichnet und nicht still falsch bedient.
  - Jedes Engine-Release gibt an, ob es das Protokoll ändert.
- **SYNC-12 (M)** Netzübersicht je Gateway:
  - letzter Kontakt;
  - Engine- und Protokollversion;
  - Rückstand der Replikation (unbestätigte Sequenzen in beide Richtungen);
  - offline seit.

  Sie ersetzt `/networks`, `last_network_contact` und den Selbsttest über `processes/ping`, der process-sync heute nach 10 min ohne Ping mit einem fatalen Fehler beendet. Künftig ist das eine Metrik mit Alarm.
- **SYNC-13 (M)** Soll- und Ist-Zustand je Deployment und Gateway sind sichtbar: ausstehend, angewendet oder fehlgeschlagen mit Fehlermeldung. Das ersetzt `state/error` und die Abfrage über `/metadata`.

---

## 18. Smart Services als Namespace

Smart Services sind der Namespace `smart-services` (NS-01, NS-02). Was heute smart-service-engine, smart-service-repository und die acht Module-Worker leisten, wird zu allgemeinen Funktionen der Engine. Jeder Namespace kann sie nutzen, soweit seine Konfiguration es erlaubt.

| Heute (Smart Services) | Künftig (alle Namespaces) |
|---|---|
| Design | Modell (Abschnitt 5) |
| Release | Deployment-Version |
| Smart-Service-Instanz | Instanz |
| Release-Info aus Formfeldern | Startparameter mit Optionen, Characteristic und Geräteauswahl-Kriterien (BPMN-02) |
| Modul | Ressource, die eine Instanz über einen Aufgabentyp angelegt hat (SMS-12) |
| Wartungsprozedur | Message-Start mit vorgegebener Instanz-ID (SMS-13) |
| Instanzvariable | Prozessvariable |
| Module-Worker | Aufgabentyp, erlaubt je Namespace (TASK-20 bis TASK-29, NS-03) |

Heute speichert das smart-service-repository (Mongo, 1 Replica) Designs, Releases, Instanzen, Instanzvariablen, Module und Wartungsprozeduren. Es deployt selbst in die smart-service-engine und stellt den Workern Rückrufe bereit (`/instances-by-process-id/...`). Mit der Integration entfallen diese Rückrufe.

- **SMS-01 (M)** Die smart-service-engine (Camunda 7.11), das smart-service-repository und die acht Module-Worker werden durch die Engine ersetzt. Ein Namespace `smart-services` wird ausgeliefert, mit dem heutigen Verhalten (NS-03, NS-05, NS-06).
- **SMS-02 (M)** Stabile IDs für Deployments und Nachrichten. Damit entfällt das heutige Umbenennen beim Release-Deploy (`id_<releaseId>`, interne Event-ID).
- **SMS-03 (M)** Beim Update eines Deployments in `smart-services` (heute Release) gilt dieselbe Wahl wie bei DEP-04: stoppen, auslaufen lassen, neu starten. Die Wahl gilt in zwei Stufen:
  - für die Instanzen selbst;
  - für die Deployments, die diese Instanzen in anderen Namespaces verwalten (NS-08). Diese erhalten die Strategie, wenn das Modul sie neu deployt.

  Heute ersetzt ein neues Release die älteren desselben Designs, auch über Besitzer hinweg, und löscht deren Deployments (Vault: „Creating a release rewrites the releases before it“). Dieses Verhalten wird nicht implizit übernommen.
- **SMS-04 (M)** Der Abschluss eines Deployments, das ein Modul angelegt hat, wird der verwaltenden Instanz direkt gemeldet. Heute sendet der Worker 5 s nach dem Deploy `deployment_done_<id>`.
- **SMS-05 (K)** Den Catch `permission_done_*` klären: Im Workspace gibt es keinen Sender, vermutlich ist er tot.
- **SMS-06 (M)** Es gibt keine verwaisten Instanzen mehr, weil alles in derselben Datenbank liegt und gemeinsam gelöscht wird. Der heutige Cleanup (`smart-service-repository/pkg/controller/cleanup.go`) entfällt.
- **SMS-07 (M)** Teilen von Deployments und Instanzen über permissions-v2, soweit der Namespace es vorsieht (NS-05). Wer ein geteiltes Deployment ausführen darf, legt eigene Instanzen davon an. So funktionieren Releases heute.
- **SMS-08 (M)** Startparameter beim Anlegen einer Instanz. Grundlage sind die Formfelder: Typ, Optionen, Mehrfachauswahl, Characteristic, Geräteauswahl-Kriterien, Reihenfolge (heute `releaseinfo.go`). Wählbare Optionen werden für den Nutzer aufgelöst, z. B. Geräte über device-selection.
- **SMS-09 (M)** Parameter einer laufenden Instanz ändern. Das löst die Update-Wahl aus SMS-03 aus (heute `PUT /instances/{id}/parameters`).
- **SMS-10 (M)** Variablen einer Instanz lesen, setzen und löschen. Sie sind die Prozessvariablen der Instanz (RUN-05); ein eigener Speicher wie heute im Repository entfällt.
- **SMS-11 (M)** Status einer Instanz: bereit, läuft, Fehler, mit Fehlermeldung. Er leitet sich aus Laufzustand und Incidents ab, statt wie heute von den Workern gemeldet zu werden.
- **SMS-12 (M)** Module: Ressourcen, die eine Instanz über einen Aufgabentyp anlegt (Deployment, Import, Analytics-Pipeline, Gerätegruppe ...), mit Typ, Daten und Löschverweis.
  - Sie sind je Instanz und gesammelt abrufbar.
  - Beim Löschen der Instanz werden sie mitgelöscht, auch Deployments auf Gateways.
  - Diese Funktion gilt in jedem Namespace, dessen Aufgabentypen Ressourcen anlegen.
- **SMS-13 (M)** Wartungsprozeduren: zusätzliche Message-Startereignisse eines Modells mit eigenen Startparametern.
  - Je Instanz auflisten, Parameter abfragen, starten.
  - Ein Start erzeugt eine Instanz mit vorgegebener ID, die auf die Instanz verweist, zu der die Prozedur gehört.
  - Der Watcher (TASK-27) löst sie intern aus.
- **SMS-14 (M)** Debugging (Abschnitt 13) gilt auch für Smart-Service-Instanzen. Heute sehen Nutzer dort nur Status und Fehlermeldung.

---

## 19. Betrieb

- **OPS-01 (S)** Mehrere Replicas sind möglich, ohne dass etwas doppelt ausgeführt wird: Timer, Zeitpläne, Watcher und Antwortverarbeitung. Konkrete Lastziele gibt es nicht (entschieden). Mehrere Replicas dienen vor allem der Verfügbarkeit bei Updates.
- **OPS-02 (M)** Persistenz in der zentralen Postgres mit Backup (entschieden). Die vier Camunda-Postgres (11.2, ohne Backup) und die Mongo-Datenbanken von process-io, Incidents, process-deployment, process-model-repository, smart-service-repository, event-deployment, Scheduler und Watcher gehen darin auf.
- **OPS-03 (M)** Die Engine bringt ihre Schemamigrationen selbst mit, versioniert und vorwärtskompatibel, für Postgres und SQLite.
- **OPS-04 (M)** Resource Requests und Limits, Liveness- und Readiness-Probe.
- **OPS-05 (M)** Prometheus-Metriken:
  - Instanzen (gestartet, beendet, abgebrochen) je Deployment und Mandant;
  - Incidents;
  - Korrelationen;
  - Kommandos und Antwortzeiten;
  - Timer-Verzug;
  - Speicher (LONG-14);

  Die heutigen Alarme werden umgestellt.
- **OPS-06 (M)** Kostenzuordnung: Der cost-calculator nutzt Pod-Namen im Namespace `process-engine` und die Metrik aus TASK-12. Die Smart-Service-Engine wird heute nicht zugeordnet; nach der Zusammenlegung läuft sie mit.
- **OPS-07 (S)** Strukturiertes Logging mit Instanz-, Lauf-, Deployment- und Element-ID, dazu Tracing über den Weg Engine → Connector → Antwort.
- **OPS-08 (M)** canary und snowflake-canary prüfen die Automation-Engine Ende-zu-Ende.

---

## 20. Migration von Camunda

- **MIG-01 (M)** Alle Deployments werden aus den gespeicherten Deployment-Modellen überführt, ohne dass Nutzer neu deployen müssen. Prozessmodelle kommen in den Namespace `processes`, Smart-Service-Designs, -Releases und -Instanzen in den Namespace `smart-services`. Die `delete_info` der Smart-Service-Module, heute URLs auf process-sync mit `wardened:`-Business-Keys, werden auf interne Verweise umgeschrieben. Modelle werden mit Version 1 angelegt, und die Zuordnung Modell–Deployment wird über Name und Mandant rekonstruiert, wo sie eindeutig ist.
- **MIG-02 (M)** Laufende Instanzen werden nach der Umstellung **neu gestartet** (entschieden), mit gleichen Startparametern.
  - Als Instanz-ID dient der bisherige Business Key, wenn er je Mandant eindeutig ist; das betrifft vor allem Smart-Service-Instanzen. Sonst wird eine neue UUID vergeben.
  - `wardened:`-Präfixe werden entfernt.
  - Prozessvariablen gehen verloren, wie beim heutigen Restart.
  - Nutzer erfahren vorher, welche Instanzen das betrifft.
- **MIG-03 (M)** Deployments, die nur in Camunda existieren, werden erfasst: Schlüssel ohne `deplid_`, in Prod 37 in der Engine gegenüber 9 im Wrapper (Vault: „Every tenant's deployments are only visible in the engine“). Für jedes wird entschieden: migrieren oder verwerfen.
- **MIG-04 (M)** Modelle werden automatisch auf die neue Ausdruckssprache umgeschrieben:
  - JUEL-Bedingungen: `${...}` entfernen, HTML-Entities auflösen. In den Exporten nutzen alle 20 verschiedenen JUEL-Bedingungen nur Vergleiche, `&&`, `||`, `!` und Indexzugriff, also eine Syntax, die JavaScript direkt versteht. JUEL-Schlüsselwörter (`eq`, `and`, `empty`) kommen nicht vor.
  - Mappings: `${var}` wird zum Ausdruck `var`, Text mit eingebettetem `${...}` zum Template-Literal.
  - Timer-Werte aus Ausdrücken (`${cycle}`, `${cron_0}`) werden zu JS-Ausdrücken.
  - `scriptFormat="groovy"` mit `println` wird zu JavaScript mit `log`.
  - `camunda:type="external"` mit Topic wird zum Aufgabentyp.
  - `http-connector` wird zu Benachrichtigung oder HTTP-Aufruf.

  Je Modell entsteht ein Bericht: was umgeschrieben wurde, was nicht unterstützt wird (`mail-send`, `R/P1D` in `timeDuration`) und was manuell zu prüfen ist.
- **MIG-05 (M)** Die Umschreibung wird gegen Camunda getestet. Bedingungen und Mappings laufen mit repräsentativen Variablenbelegungen in beiden Engines und müssen dasselbe Ergebnis liefern. Grund sind Unterschiede in der Typumwandlung zwischen JUEL und JavaScript, etwa bei `==` zwischen Zahl und String oder bei `null`.
- **MIG-06 (M)** Für die Gateways gibt es einen Rollout über ein Update des Prozess-Moduls, mit Übernahme lokaler Deployments und IO-Daten.
  - Die angesammelte Camunda-Historie wird nicht übernommen.
  - Die alten Postgres-Volumes werden entfernt.
  - Ein Parallelbetrieb mit alten Gateway-Versionen ist nicht vorgesehen; alte Versionen und arm/v7 sind irrelevant (entschieden).
- **MIG-07 (S)** Parallelbetrieb in der Cloud: Neue Deployments gehen an die Automation-Engine, bestehende laufen in Camunda aus. Da laufende Instanzen ohnehin neu gestartet werden (MIG-02), genügt auch ein Stichtag.
- **MIG-08 (M)** Rechte werden übernommen. Die Freigaben aus den Topics `processmodel`, `smart_service_releases` und `smart_service_instances` gehen in die neuen Topics über (NS-05), gleiche Nutzer, Gruppen und Rechte: `processmodel` nach `processes_models`, `smart_service_releases` nach `smart-services_deployments`, `smart_service_instances` nach `smart-services_instances`. Smart-Service-Designs haben heute kein Topic, sie sind nur für den Besitzer sichtbar (`smart-service-repository/pkg/controller/designs.go` filtert nach User-ID). Sie erhalten die Default-Permissions von `smart-services_models`. Deployments und Instanzen im Namespace `processes` erhalten die Default-Permissions, also heute nur den Besitzer.
- **MIG-09 (M)** Owner werden aus den heutigen Daten übernommen:
  - Modelle: Feld `owner` im process-model-repository;
  - Deployments: `owner` in process-deployment, bzw. die Camunda-`tenantId` bei Deployments, die nur in Camunda existieren (MIG-03);
  - Smart-Service-Designs, -Releases und -Instanzen: ihre User-ID bzw. ihr `creator`.

  Auf Prod gibt es Releases mit leerem `creator` und mit `placeholder-for-invalid-data` (Vault: „A release creator can be empty or a placeholder“). Solche Ressourcen bekommen keinen erfundenen Owner. Sie werden aufgelistet und von Hand zugeordnet oder gelöscht. Hat ein übernommener Owner in permissions-v2 kein Administrieren, wird es gesetzt (SEC-10).
---

## 21. Weitere Komponenten, die betroffen sind

| Komponente | Betroffenheit |
|---|---|
| **web-ui** | Monitor, Deployments, Prozessmodelle, Widgets (Switch, Range-Slider, Process-State, Charts, Incident-Liste, Scheduler), Fog-Ansichten, Ladon-Admin mit hart codierten Pfaden; Widgets `data-table` und `device-status` erzeugen versteckte Deployments (`source=generated`). Neu: Umstellung auf die neue API (API-04), Namespaces, Update-Dialog mit Strategie (DEP-04), Modellversionen (MOD-05), Live- und historisches Debugging (Abschnitt 13), Aufbewahrungseinstellungen (HIST-04); die API setzt das web-ui selbst um, auf Basis der OpenAPI (API-07), ohne eigene Client-Bibliothek |
| **Prozess-Designer im web-ui** | schreibt heute den Camunda-Dialekt über `camunda-bpmn-moddle` mit voller Palette; künftig nur die unterstützte Menge (BPMN-18), nur JavaScript (EXPR-01), Aufgabentypen statt Topics, Benachrichtigung und HTTP-Aufruf als eigene Typen. Ein Designer für alle Namespaces, konfiguriert über den Namespace (NS-09), statt Prozess- und Smart-Service-Designer |
| **dashboard** | speichert Widget-Typen mit Prozessbezug |
| **canary, snowflake-canary** | externe API-Konsumenten, Monitoring; stellen auf den Go-Client um (CLI-08) |
| **cost-calculator, opencost-wrapper** | Pod-Namen und Metrik-Labels |
| **models** (`models/go`) | geteiltes Deployment-Modell; mit CLI-01 zu klären, ob die Typen der Engine dort oder im Engine-Repo liegen |
| **platform-connector-lib** | `task_info` mit Prozess-IDs im Kommando-Umschlag |
| **device-command, live-data-worker** | importieren Pakete aus `external-task-worker/lib`; die Pakete ziehen an einen stabilen Ort um (live-data-worker ist nicht deployt) |
| **smart-service-module-worker-lib** | entfällt mit der Integration; prüfen, ob andere Repos sie importieren |
| **timescale-wrapper, timescale-rule-manager** | Quelle für die Rekonstruktion von Event-Auswertungen (EVT-10) und den aktuellen Wert (DBG-18); deren Aufbewahrung begrenzt das Event-Debugging |
| **analytics-flow-engine, flow-parser, import-deploy, serving, device-manager, device-selection** | Ziele der integrierten Smart-Service-Module; deren Clients wandern in die Engine |
| **mgw-device-cloud-connector** | bleibt als allgemeines MQTT-Relay des Gateways für `processes/...`; Upstream-QoS 0 und Puffer nur im Speicher müssen für SYNC-02 geändert werden |
| **mgw-module-repository** (`process/Modfile.yaml`) | Gateway-Modul mit heute 6 Containern plus 2 Postgres; künftig Engine (mit Synchronisation, Aufgabentypen und IO) und event-worker, ohne eigene Datenbank-Container |
| **process-sync, mgw-process-sync-client** | gehen in der Engine auf (Abschnitt 17); Repos werden archiviert |
| **user-management** | ruft heute process-scheduler beim Löschen auf, sonst Kafka `user`; künftig Vorabprüfung und Löschaufruf der Engine (SEC-14) |
| **notifier, developer-notifications** | Ziel von Benachrichtigungen und Incident-Meldungen |
| **Kong, Ladon** | eine Route für die neue API plus `deprecated/`; die heutigen Routen `/process/engine`, `/process/deployment`, `/process/repository`, `/process/scheduler`, `/process/incidents`, `/process-io`, `/process-sync`, `/process-fog-deployment`, `/smart-services/repository` leiten auf `deprecated/` um (API-08); WebSocket-Upgrade für DBG-01; tote Routen `/api-aggregator`, `/eventmanager` |
| **Kafka-Topics** | `process-deployment-done` bleibt oder wird ersetzt; `deployments_v3`, `camunda_incident` und vermutlich `processmodel` sind Altlast |
| **ArgoCD** | Apps `process-engine-prod`, `smart-service-engine-prod` |
| **api-aggregator** | abschalten |

---

## 22. Bei der Umsetzung zu prüfen

Offene Entscheidungen gibt es derzeit keine. Folgende Punkte sind nicht geprüft:

- **Owner-Schutz in permissions-v2 (SEC-10):** Lässt sich ein Entzug des Owner-Rechts in permissions-v2 verhindern, z. B. mit einer Topic-Option, die den Owner schützt? Oder muss die Engine auf Rechteänderungen reagieren, etwa über das Kafka-Topic aus `PublishToKafkaTopic`, und das Recht wiederherstellen? Davon profitieren auch Geräte und Hubs.

- **Timescale-Aufbewahrung:** Auf welche Tabellen wendet timescale-rule-manager Aufbewahrungsregeln an (z. B. `retention-one-year`)? Das begrenzt, wie weit die Rekonstruktion von Event-Auswertungen zurückreicht (EVT-12).
- **Importdaten:** Landen Importdaten in Timescale? Davon hängt ab, ob Import-Events rekonstruiert werden können (EVT-12).
- **Fog-Gerätedaten:** Kommen die vom Gateway weitergeleiteten Geräteevents vollständig in Timescale an? Dazu gehört die Zuordnung lokaler IDs zu Cloud-IDs (EVT-13).
- **Gleiche Auswertung in Rekonstruktion und Betrieb:** Liefert die Auswertung auf Timescale-Werten dasselbe Ergebnis wie auf den Kafka-Nachrichten? Zu prüfen sind Pfade, Umrechnung und Zeitstempel (EVT-10).
- **Weitere Nutzer von `smart-service-module-worker-lib`:** Gibt es sie außerhalb der acht Worker?

## Hinweise zur Quellenlage

- **Exportierte Modelle** (`~/Downloads/bpmn`, 63 Dateien): möglicherweise veraltet. Ob die Modelle mit `mail-send` und Groovy noch deployt sind, ist offen. Deployte Fassungen (`xml_deployed`) und Smart-Service-Releases wurden nicht ausgewertet.
- **process-scheduler**: Der lokale Checkout stammt vom 2026-08-27 und hat das Prod-Tag `v1.0.0` nicht. Die Aussagen beruhen auf HEAD.
- **event-deployment**: Der lokale Branch `dev` steht hinter Prod (`v0.2.0`). Gelesen wurde `v0.2.0`.
- **process-sync**: Ausgecheckt ist der nicht gemergte Branch `feat/platform-source-of-truth`. Prod läuft auf `v1.0.2`.
- **mgw-process-module**: Das Repo ist veraltet (v1.3.1). Maßgeblich ist `mgw-module-repository/main/process/Modfile.yaml` (v1.5.4).
- **Vault-Notizen, die dem Code widersprechen**:
  - Process-Deployment im privaten und im geteilten Vault: Dort heißt es, das Deployment-Kommando laufe über `deployments_v3`. Tatsächlich läuft es über HTTP; Kafka ist abgeschaltet (`DEPLOYMENT_TOPIC="-"`, `DISABLE_KAFKA=true`).
  - „Creating a process deployment through the api“: Dort heißt es, die Engine erfahre asynchron vom Deployment. Tatsächlich deployt der Wrapper synchron im Request.
