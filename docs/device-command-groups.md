# Device commands to several devices

How a `device_command` task proceeds when it addresses more than one device service: a device group, or a device selected without a service. Each matching combination of device and service (function plus every aspect of the task, TASK-06) is a sub-task, stored with the job in `command_subtasks`, together with its number of tries and the time of its last try. `GROUP_SCHEDULER` (`parallel`, `sequential` or `round_robin`, default `sequential`) decides the order.

## Scope

Holds for device commands with two or more sub-tasks, pessimistic completion, from `f4a4189` on. A command to a single device service, an optimistic command and the reading of last values without a command (`prefer_event`, event services) are not affected by the scheduler. **Not this if** the task fails before any command is sent, e.g. because device-repository or the token exchange are unavailable: that is an error of the execution and is retried with backoff like any task.

## The terms

- **Timeout**: the task timeout (`TASK_TIMEOUT`, or the timeout of the element). For `sequential` and `round_robin` it is the answer window of **one try of one sub-task**, not a limit for the whole task.
- **Retries**: the `retries` of the payload; without it the retries of the element or `TASK_RETRIES`. For `sequential` and `round_robin` they count **per sub-task**: every sub-task gets `retries + 1` tries.

## parallel

All sub-tasks are sent at once. A timeout uses up one attempt of the task; the next attempt sends again only to the sub-tasks without an answer. After the last attempt the task completes with the answers that arrived, or becomes an incident if none did.

## sequential and round_robin

One sub-task at a time:

1. While a sent command is no older than the timeout, the task waits for its answer. A timeout of the job then keeps the task waiting without using up an attempt and without backoff: the engine asks the task type (`TimeoutHandler`), which answers with `Async` and the time its window ends (`TaskResult.WaitUntil`).
2. An answer, or the end of the window, leads to the next sub-task. `sequential` takes them in their order; `round_robin` takes the one with the fewest tries first, so that the tries spread across the devices.
3. A sub-task without an answer after `retries + 1` tries is given up, and the next one is asked straight away.
4. When every sub-task has an answer or is given up, the task completes with the answers in the order of the sub-tasks. If no sub-task answered at all, it becomes an incident with `no answer from any device within <timeout>, <n> tries each`.

A group therefore takes at most `sub-tasks × (retries + 1)` windows. An error answer of a device (`errors` topic or `POST /errors`) and an error while sending count as a failed attempt of the task, with backoff, as for every task type.

This follows `lib/devicegroups` of external-task-worker, where a Camunda lock expiry was not a retry and each sub-task counted its own tries. Until `f4a4189` the engine counted timeouts per task, so a silent device used up the attempts of the whole group and the remaining devices were never asked; `pkg/tasks/devicecommand/groups_order_test.go` holds the cases.
