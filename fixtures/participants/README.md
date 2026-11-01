# Facilitator script

This script is for the facilitator of the operator usability sessions of ginsys/bronzeward#263
(decision V10, owner decision BW-263-participants-1). Participants never read it. It states each
task's prompt, its probe and its measures. It gives no step of any task: a step that the
facilitator says is a hint, and a hint makes the task "not completed unaided".

The tasks are those of [journeys §13](https://github.com/ginsys/bronzeward/blob/daa4393b648aeba0e166cfdec6f000be060b389f/docs/spec/operator-experience.md#13-usability-and-accessibility-acceptance)
at `daa4393`. Everything in the environment is synthetic.

## Sessions

- Each participant runs all eleven tasks in both clients, in two sessions of about two hours, on
  different days. One session uses `bronzewardctl`, the other the web interface.
- Alternate the client order: P1, P3 and P5 start with `bronzewardctl`; P2 and P4 start with the
  web interface.
- Record participants as P1 to P5 only. Write no name, account or contact detail in any record.
- Make no audio, video or screen recording. Keep the facilitator's notes and the client output,
  on synthetic data only.
- The participant uses the fixture host directly, in person or by screen share, with the packaged
  `bronzewardctl` and Chrome or Firefox. The operator documentation is open to them. The
  specifications are not.

## Before each session

1. Recreate the fixture: `fixtures/participants/session stop`, `fixtures/bin/down`, then
   `fixtures/participants/session start`. If this checkout already runs a dev-idp provider, start
   refuses: run `mise run dev-idp-down` first. Start also refuses while an earlier session's state
   exists: run `session stop` first. Keep this order: run `session stop` before `fixtures/bin/down`
   or `fixtures/dev-idp down`, and run one participant session per Docker daemon. The session
   uses the host's one default dev-idp, so no other checkout may start or stop the default dev-idp
   on that daemon while a session starts or runs. `session stop`, and the rollback of a failed
   `session start`, remove the forwarder and dev-idp by name, so outside this order they can
   remove another checkout's containers.
2. Generate this session's passwords: `fixtures/participants/session passwords`. It shows each
   password once, on the terminal only. Give the participant the password of each account when a
   task needs it. Write none down. Tell the participant to refuse when the browser offers to save
   a password.
3. Open the participant's terminal with `fixtures/participants/session shell`. It has its own
   client configuration and keeps no command history.
4. Run the setup command of the first task: `fixtures/participants/session t1`. Each setup command
   prints the names to read out with the prompt (a machine, a field, a label value) and the reads
   that confirm its precondition. If a read fails, stop and recreate the fixture. Only T1 has a
   setup command yet. The setup commands of T2 to T11 come in a later change under #367.

## After each session

Run `fixtures/participants/session scan`, and name the browser profile's directory as an argument.
It searches the fixture, provider and participant state and the containers' logs for a session
password. It first plants a control that it must find. If it reports a password, record that in
the session notes and recreate the fixture before the next session.

## Running a task

1. Run the task's setup command. Do not show its output to the participant.
2. Read the prompt aloud, word for word, with the names the setup command printed. Start the
   time at the end of the prompt.
3. Do not answer questions about how to do the task. If the participant asks, say: "Do what you
   would do on your own." If the participant asks to stop, stop the time and record the task as
   not completed.
4. Stop the time when the participant says that the task is done, or stops. Then compare the
   result with "Done when". Do not tell the participant whether the result is correct.
5. Ask the probe, word for word, and write the answer down word for word.
6. Fill in the measures record for that task.

## Measures

One record per participant, client and task, in `measures.yaml`'s form:

- **Completed unaided:** yes or no. "Done when" holds and no hint was given.
- **Time:** from the end of the prompt to "done" or the stop, in seconds.
- **Refusals hit:** the number of refusals that the client showed, counted from its output.
- **Help lookups:** the documentation pages opened and the `help` invocations, counted by the
  observer.
- **Probe answer:** word for word, and whether it matches "Expected".

## Tasks

The role is the account the participant uses. "Done when" and "Expected" are for the
facilitator only. Setup `tN` means `fixtures/participants/session tN`.

### T1: sign in

- Role: any. Setup: `t1`.
- Prompt: "You have access to a Bronzeward server. Sign in to it, and tell me which server you
  are working with and which roles you hold."
- Done when: the correct context and roles are stated.
- Probe: none.

### T2: the drifted machine

- Role: viewer. Setup: `t2`.
- Prompt: "One machine in the cluster has drifted. Find it, and tell me its desired release, its
  applied release, its observed release and when it was last observed."
- Done when: all four are correct.
- Probe: "Is it healthy?" Expected: not established.

### T3: import a machine

- Roles: author, publisher, approver. Setup: `t3`.
- Prompt: "Machine `<machine>` is not managed by Bronzeward yet. Import it. Its configuration
  holds a secret value that is not marked as secret. Make sure that it is kept as a secret. Then
  get the machine adopted."
- Done when: the import is released, the mark is recorded and the adoption set `Applied`.
- Probe: "Is the secret in the draft?" Expected: a reference only.

### T4: change a node label

- Role: author. Setup: `t4`.
- Prompt: "Give the worker machines the node label `<label>` with the value `<value>`, and save
  the change."
- Done when: the draft entry is saved.
- Probe: "Has any machine changed?" Expected: no.

### T5: a concurrent edit

- Role: author. Setup: `t5`, then `t5 collide` after the participant starts the edit.
- Prompt: "Change the value of the node label `<label>` to `<value>`, and save it."
- Done when: the other change is read and the participant's own change is applied again.
- Probe: none.

### T6: publish and find the rendered configuration

- Role: publisher. Setup: `t6`.
- Prompt: "Publish the saved change. Then find the configuration that Bronzeward renders for
  machine `<machine>`."
- Done when: the review is open for that machine.
- Probe: "Are the sources the configuration?" Expected: no.

### T7: an empty-looking field and a redacted field

- Role: publisher. Setup: `t7`.
- Prompt: "In that review, field `<empty field>` looks empty and field `<redacted field>` is
  hidden. Tell me why each one is shown that way."
- Done when: each is explained from the token or the provenance.
- Probe: "Why is this empty?" Expected: an answer from the metadata, or "empty in the rendered
  review". "Empty in the source" is correct only after the participant read that source
  revision.

### T8: plan and approve

- Roles: publisher, approver. Setup: `t8`.
- Prompt: "Prepare the published change for the workers and approve it. Before you approve,
  tell me what will change."
- Done when: the plan is approved and the change is described from the diff.
- Probe: "Was the imported configuration changed?" Expected: only what the diff shows.

### T9: follow the apply

- Role: viewer. Setup: `t9`, then `t9 disconnect` while the participant follows the apply.
- Prompt: "The approved change is being applied. Follow it until it finishes, and tell me when
  it finished."
- Done when: the completion is reported with its time.
- Probe: none.

### T10: a drifted machine

- Roles: author, then approver. Setup: `t10`.
- Prompt: "Machine `<machine>` has drifted. Stop Bronzeward from changing it. Then decide
  whether to put it back to its desired configuration or to keep its current configuration, and
  carry that out."
- Done when: the drift is closed as the participant chose.
- Probe: "Did detection revert it?" Expected: no.

### T11: recovery

- Role: recovery-admin. Setup: `t11`.
- Prompt: "The server was restored from a backup and is in recovery mode. One scope is not
  accounted for. Account for it, then release it."
- Done when: the scope is released with stated evidence.
- Probe: "Is release an approval?" Expected: no.
