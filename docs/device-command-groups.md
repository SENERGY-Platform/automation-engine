# Device commands to several devices

How a `device_command` task proceeds when it addresses more than one device service: a device group, or a device selected without a service. Each matching combination of device and service (function plus every aspect of the task, TASK-06) is a sub-task, stored with the job in `command_subtasks`, together with its number of tries and the time of its last try. `GROUP_SCHEDULER` (`parallel`, `sequential` or `round_robin`, default `sequential`) decides the order, `GROUP_CONCURRENCY` how many sub-tasks of `sequential` and `round_robin` wait for an answer at the same time.

## Scope

Holds for device commands with two or more sub-tasks, pessimistic completion, from `f4a4189` on. A command to a single device service, an optimistic command and the reading of last values without a command (`prefer_event`, event services) are not affected by the scheduler. **Not this if** the task fails before any command is sent, e.g. because device-repository or the token exchange are unavailable: that is an error of the execution and is retried with backoff like any task.

## The terms

- **Timeout**: the task timeout (`TASK_TIMEOUT`, or the timeout of the element). For `sequential` and `round_robin` it is the answer window of **one try of one sub-task**, not a limit for the whole task.
- **Retries**: the `retries` of the payload; without it the retries of the element or `TASK_RETRIES`. For `sequential` and `round_robin` they count **per sub-task**: every sub-task gets `retries + 1` tries.
- **Concurrency**: `senergy:groupConcurrency` of the element, a positive integer checked at deploy; without it `GROUP_CONCURRENCY` (default `1`). The number of sub-tasks with an **open try**, a sent command whose window has not ended and that has no answer yet. Per element because `GROUP_SCHEDULER` and `GROUP_CONCURRENCY` hold for every group of an engine, while how many commands a group of devices takes at once depends on the devices (SNRGY-5093).

## parallel

All sub-tasks are sent at once. A timeout uses up one attempt of the task; the next attempt sends again only to the sub-tasks without an answer. After the last attempt the task completes with the answers that arrived, or becomes an incident if none did.

## sequential and round_robin

Up to the concurrency of sub-tasks at a time, one with the default `1`:

1. The task sends sub-tasks until as many tries are open as the concurrency allows. `sequential` takes them in their order; `round_robin` takes those with the fewest tries first, so that the tries spread across the devices. A sub-task with an open try is not sent again.
2. While every place is taken, or nothing but open tries is left, the task waits. A timeout of the job then keeps the task waiting without using up an attempt and without backoff: the engine asks the task type (`TimeoutHandler`), which answers with `Async` and the earliest time one of the open windows ends (`TaskResult.WaitUntil`). With a concurrency above `1`, an execution of a group that leaves tries open from before returns that time as well, so that the job does not wake only at the end of its own timeout; with `1` the timing is the same as before the setting.
3. An answer, or the end of a window, frees a place and the next sub-task is sent. Choosing a sub-task and counting its try happen in one transaction under the lock of the instance, before the command goes out, so that answers arriving at the same time and an execution never send a sub-task twice or open more tries than places.
4. A sub-task without an answer after `retries + 1` tries is given up, and the next one is asked straight away.
5. When every sub-task has an answer or is given up, the task completes with the answers in the order of the sub-tasks. If no sub-task answered at all, it becomes an incident with `no answer from any device within <timeout>, <n> tries each`.

With concurrency `1` a group therefore takes at most `sub-tasks × (retries + 1)` windows, and with `sequential` a device without radio contact holds up all devices after it for `retries + 1` windows, with `round_robin` for one window per round of tries. With concurrency `n` that device takes one place and the others go on in the remaining ones; with `sequential` its next try comes right after its window, with `round_robin` after the devices with fewer tries.

Measured on a gateway with nine Homematic IP shutters behind one CCU (2026-11-05): all commands at once (`parallel`) are worked off by the CCU one after the other, the acknowledgements spread over 5 s and some arrive only after 17 s; `sequential` with concurrency `1` and a real acknowledgement takes 1.8 s for all nine, about 130 ms each. A concurrency above `1` has not been measured there yet. An error answer of a device (`errors` topic or `POST /errors`) and an error while sending count as a failed attempt of the task, with backoff, as for every task type.

This follows `lib/devicegroups` of external-task-worker, where a Camunda lock expiry was not a retry and each sub-task counted its own tries. Until `f4a4189` the engine counted timeouts per task, so a silent device used up the attempts of the whole group and the remaining devices were never asked; `pkg/tasks/devicecommand/groups_order_test.go` holds the cases.
