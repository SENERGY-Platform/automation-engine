# Maintenance procedures of smart services

A maintenance procedure is an additional message start event in the model of a smart service. It starts a short run that belongs to an existing instance and changes what that instance created, without running the instance again from its main start (SMS-13).

## Scope

Holds for the namespace `smart-services` of the engine, read from the code of `v0.43.4`. The engine took this over from smart-service-repository. The old API under `/deprecated/smart-services` lists the procedures of a release (`maintenance_procedures`) and the running ones of an instance (`running_maintenance_ids`), but has no start. **Not this if** a message start event in `processes` is meant: there it is an ordinary start through `/messages`, without a parent instance.

## What counts as one

Every start event of the model with a message definition and a message name, except a device event, is a procedure. Its id is the message name, its parameters are the form fields of that start event, as for the main start. `GET /namespaces/{ns}/instances/{id}/maintenance-procedures` lists them for the version the instance runs on.

## Starting one

- `POST /namespaces/{ns}/instances/{id}/maintenance-procedures/{procedure}/start` with `{"variables": {...}}`. It needs the administrate right on the instance. Message starts in `smart-services` start only this way, not through `/messages`.
- The `watcher` task starts the procedure named in `watcher.maintenance_procedure` when its device selection, device group or request answers differently than at its last check (TASK-27), with the inputs `watcher.maintenance_procedure_inputs.*`. Watches of one instance that fire in the same pass for the same procedure and inputs start it once. While a run of that procedure is running, a change queues a rerun instead of a second run; the rerun starts once no run of the procedure is running, checked every 5 s. A run in an incident has ended and holds nothing back, unless an automatic restart of it is pending. Further changes during the run join the queued rerun, a rerun with other inputs is queued apart (SNRGY-5143).
- The web-ui starts `accept_suggestion` when a user accepts an automation suggestion in the widget of that name. There is no general button for procedures.

A start is refused with 409 unless the instance is `ready`: its main run completed and none of its modules has an error. A procedure cannot be started on a procedure (400), and not on an instance of a gateway; smart services run in the cloud only.

## What the run sees and changes

- It is an instance of its own with the parent in `parent_id`, on the same deployment version. Its start variables are those of the parent, overlaid with the given ones; `auto_select_all` fields are filled as at a start.
- Module tasks in it work on the parent: they read the modules and variables of the parent, and the modules they create belong to the parent. The task types `process_deployment`, `analytics`, `info` and the device group task of `device_repository` take a `key` input: with the key of an existing module of the parent they update that module and its resource instead of creating a second one. That is how a procedure moves, say, the deployed process to new devices.
- Several procedures of one instance may run at the same time. Only the watcher holds back a second run of the same procedure, also across replicas; a start through the API starts beside a running run, which then holds back the watcher.

## Updates

Nothing starts a procedure because of an update.

- A new release of a design is a new version of its deployment with the strategy `drain`: existing instances keep running on their version and keep its procedures.
- Changing the parameters of an instance (`PUT .../instances/{id}/parameters`) starts a new run of the instance from its main start, on the version given in `deployment_version`, without one on the current version of the deployment, so a parameter change also moves the instance to the newest release. The run removes the modules of the previous one and deletes its maintenance procedures, also running ones, and the reruns queued for them. The procedures available afterwards are those of the version of the new run.

Whatever a procedure changed is therefore gone after an update unless the main run creates it again.
