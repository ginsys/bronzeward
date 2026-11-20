# Web accessibility record

The WCAG 2.2 AA items of [journeys §13](../docs/spec/operator-experience.md#13-usability-and-accessibility-acceptance)
on the views of [#334](https://github.com/ginsys/bronzeward/issues/334), for usability tasks T1
(sign in, say which roles you hold) and T2 (find the drifted machine and state its desired,
applied and observed release and observation time), per browser. Coverage is the one the owner
accepted in [#255](https://github.com/ginsys/bronzeward/issues/255): keyboard on every tested
browser; screen reader on VoiceOver with Safari, and on NVDA with Firefox and with Chrome.

An automated check is evidence for what it measures, not conformance (§13). Items whose views
come with later tickets (reflow of the diff in review) are recorded by those tickets. The import
wizard, the protected import review and the conflict view of the draft editor are recorded in the
section "Import wizard, protected review and conflict view", the publish screen in "Publish screen
(T3)", and the plan page and its approval in "Plan and approval screen (T8)", all of the keyboard and
measured checks.

## Keyboard and measured checks

Automated, in `web/e2e/auth/inventory.e2e.ts` ("keyboard only", "reflow at 320 CSS pixels" and
the W2/W3 and list cases), run by `mise run browser-auth` against a disposable deployment with
synthetic data. Recorded on 28 October 2026 with Chrome 139, Edge 143, Firefox 159 and Firefox
ESR 153 on Linux.

| Item | What is checked | chrome | edge | firefox | firefox-esr |
| --- | --- | --- | --- | --- | --- |
| 2.1.1 Keyboard, 2.1.2 No keyboard trap | Tab, Shift+Tab, Enter and the arrow keys alone: the skip link, the machine list to a machine (T2), Sign out and the navigation (T1), a list's link to its item and on to a revision, a filter set into the query; Tab moves on from every control | pass | pass | pass | pass |
| 2.4.7 Focus visible | every focus Tab reaches shows an outline of at least 2 px | pass | pass | pass | pass |
| 2.4.3 Focus order | a navigation moves focus to the new page, and Tab continues there in reading order | pass | pass | pass | pass |
| 2.4.11 Focus not obscured | nothing is fixed or sticky, so the header and banners never cover focus | by construction | | | |
| 4.1.3 Status messages | every state text is mirrored into the page's one hidden `role="status"` region, announced without moving focus; a server refusal is an alert on its own element; the empty machine list checks the mirror and the Changes page checks that two messages share one region | pass | pass | pass | pass |
| 1.4.3 Contrast | the lowest contrast of any shown text, measured in the page, at least 4.5:1 | pass | pass | pass | pass |
| 1.4.11 Non-text contrast | focus outline `#3dd68c` on the page `#0c1017` 10.2:1; table rules and control borders `#718096` on the page 4.75:1, on a card 4.35:1 | by computation | | | |
| 1.4.10 Reflow | at 320 CSS pixels no page scrolls sideways: machines, plans and a plan with its diff, sources, a fragment and its revision's document, operations and an operation; a table scrolls in its own labelled, focusable region | pass | pass | pass | pass |
| 2.5.8 Target size | every button, select and navigation link at least 24 by 24 px | pass | pass | pass | pass |
| 1.4.1 Use of colour | drift, freeze, staleness and failure are words; the views set no colour with meaning | by construction; the words in `web/src/*.test.ts` | | | |
| 3.3.1 Error identification | these views have no input field | not applicable | | | |

The release page and the rendered review of one of its machines
([#342](https://github.com/ginsys/bronzeward/issues/342)) have their own cases in the same file
("the rendered review of a release's machine"), run by the same command on the same browsers:

| Item | What is checked on the release page and its rendered review |
| --- | --- |
| 2.1.1 Keyboard, 2.4.7 Focus visible | Tab reaches the release in the list, then the link to a machine's rendered configuration; Enter opens it and focus moves to the page |
| 1.4.3 Contrast, 1.4.11 Non-text contrast | the lowest contrast of any shown text, redaction badges included, is at least 4.5:1; the badge border is `#3dd68c`, 9.31:1 against the `pre` it sits in (`#141a24`) and 8.37:1 against its own fill (`#1c2333`) |
| 1.4.1 Use of colour | a redaction token shows as `[secret registry/example-pass, version 1]`: its kind is words inside a border, never colour alone |
| 1.4.10 Reflow | at 320 CSS pixels neither page scrolls sideways; the provenance table scrolls in its own labelled region |
| 2.5.8 Target size | every button, select and navigation link at least 24 by 24 px |

### Import wizard, protected review and conflict view

The tasks T3 (the import wizard, the import page and the protected review) and T5 (the conflict view of
the draft editor), from [#894](https://github.com/ginsys/bronzeward/issues/894). Automated, in
`web/e2e/auth/a11y-import.e2e.ts`, run by `mise run browser-auth` against a disposable deployment
with synthetic data. Recorded on 18 November 2026 with Chrome 157, Edge 157, Firefox 160 and Firefox
ESR 153 on Linux, from the `browser-auth` jobs of the pull request that added the cases
([#903](https://github.com/ginsys/bronzeward/pull/903)). The focus rows were recorded on 19 November 2026 with the same four
versions, from the jobs of [#905](https://github.com/ginsys/bronzeward/pull/905), which fixed the focus defects of
[#896](https://github.com/ginsys/bronzeward/issues/896). A failure names its issue.
The cases keep nothing protected: the storage check, the HTTP cache check and the artifact scan hold
after each case.

| Item | Screen and what is checked | chrome | edge | firefox | firefox-esr |
| --- | --- | --- | --- | --- | --- |
| 2.1.1 Keyboard, 2.1.2 No keyboard trap | wizard: the Changes and Imports links, Start import, the machine and draft selects and the source and staging radios by arrow keys, a typed document, Start import; Tab leaves every control | pass | pass | pass | pass |
| 2.1.1 Keyboard, 2.1.2 No keyboard trap | protected review: Review import, a Mark as secret button, Send marks, Continue import, and Abandon import by Enter | pass | pass | pass | pass |
| 2.1.1 Keyboard, 2.1.2 No keyboard trap | conflict view: the entry, Show their change, Copy my text and Save by Tab and Enter | pass | pass | pass | pass |
| 2.1.1 Keyboard | conflict view: Save is a Tab stop in the conflict state, reached by Tab from the entry before the refused save and from the Document text after the copy | pass | pass | pass | pass |
| 2.4.7 Focus visible | wizard, protected review, conflict view: every focus Tab reaches shows an outline of at least 2 px | pass | pass | pass | pass |
| 2.4.3 Focus order | wizard: a page change moves focus to the new page, and Tab reaches the named controls from there | pass | pass | pass | pass |
| 2.4.3 Focus order | protected review: focus is on the review's text after Review import, in the claim section after Send marks, Continue import and Abandon import, and in the review's section after Close review | pass | pass | pass | pass |
| 2.4.3 Focus order | conflict view: focus is in the editor pane after a refused save and after the saved text, and on the Document text after Copy my text; Show their change keeps focus | pass | pass | pass | pass |
| 4.1.3 Status messages | wizard: the refused start and the released import; protected review: the open review; conflict view: the refused save; each shown in a `role="status"` region or an alert, and the wizard's released-import page holds exactly one such region. That focus stays put after each is part of the 2.4.3 rows | pass | pass | pass | pass |
| 1.4.3 Contrast | wizard, protected review, conflict view: the lowest contrast of any shown text, measured in the page, at least 4.5:1 | pass | pass | pass | pass |
| 1.4.10 Reflow | at 320 CSS pixels no page scrolls sideways: the import wizard with a long draft title, the import page, the paused import, its open review and marks panel, and the conflict view with their text and mine | pass | pass | pass | pass |
| 2.5.8 Target size | wizard, protected review, conflict view: every button, select and navigation link at least 24 by 24 px | pass | pass | pass | pass |
| 3.3.1 Error identification | wizard: a start with no document is refused in text beside the button, "Not sent: paste the document or choose its file." | pass | pass | pass | pass |

Local Chrome 139 runs had found focus dropping to the page body after the actions of the protected
review and the conflict view ([#896](https://github.com/ginsys/bronzeward/issues/896)); the focus
assertions failed on the unchanged product and pass on all four browsers since
[#905](https://github.com/ginsys/bronzeward/pull/905).

### Publish screen (T3)

The publish screen of T3 (the draft's Publish this draft link, the publication page and the release
page it names), from [#897](https://github.com/ginsys/bronzeward/issues/897). Automated, in
`web/e2e/auth/publish.e2e.ts`, run by `mise run browser-auth` against a disposable deployment with
synthetic data. The deployment has no Talos node, so a succeeded publication is served by the
injector: the page's own requests are real, the operation's answers are synthetic. Recorded on 19
November 2026 with Chrome 157, Edge 157, Firefox 160 and Firefox ESR 153 on Linux, from the
`browser-auth` jobs of [#910](https://github.com/ginsys/bronzeward/pull/910). The first keyboard run
failed on the unchanged product (Chrome 139, local): Publish is disabled once the write is accepted,
so focus fell to the page body; the page's answer now takes it.

| Item | Screen and what is checked | chrome | edge | firefox | firefox-esr |
| --- | --- | --- | --- | --- | --- |
| 2.1.1 Keyboard, 2.1.2 No keyboard trap | from the draft to the release by Tab and Enter alone: the Changes link, the draft's link, Publish this draft, Publish and the release link; Tab reaches each of them and never stays on one stop | pass | pass | pass | pass |
| 2.4.7 Focus visible | every focus Tab reaches shows an outline of at least 2 px | pass | pass | pass | pass |
| 2.4.3 Focus order | each page change moves focus to the new page; after Publish, and again when the operation ends, focus is in the page's answer and not on the page body; Tab continues to the release link | pass | pass | pass | pass |
| 4.1.3 Status messages | the accepted line is mirrored into a `role="status"` region, and the page holds exactly one such region; where focus goes after Publish is in the 2.4.3 row | pass | pass | pass | pass |
| 1.4.3 Contrast | the publication page with its accepted line and its end: the lowest contrast of any shown text, measured in the page, at least 4.5:1 | pass | pass | pass | pass |
| 1.4.10 Reflow | at 320 CSS pixels no page scrolls sideways: the publication page with a long draft title, the accepted line, the end and the release page | pass | pass | pass | pass |
| 2.5.8 Target size | the publication page: every button, select and navigation link at least 24 by 24 px | pass | pass | pass | pass |

Not run here: the refusal paths of the page by keyboard. The 412 "Read the draft again" alert sits in
the same focus wrapper as the accepted line, and the no-ETag note sits before it; neither has a case of
its own.

The previous major of each browser (B6) ran the same specs, and every row above passes on Chrome 156.0.8078.80, Edge 156.0.4314.58 and Firefox 159.0.1 ([run of 19 November 2026](https://github.com/ginsys/bronzeward/actions/runs/44437080904), 14 of 14 spec files in each browser; the builds and sources are in the [browsers section of the conformance README](README.md#browsers)).

Safari 27.2 on a GitHub-hosted macOS runner ran the same specs too ([run of 19 November 2026](https://github.com/ginsys/bronzeward/actions/runs/44556204194), 15 of 15 spec files pass; ginsys/bronzeward#892 and ginsys/bronzeward#927, the setup is in the [browsers section of the conformance README](README.md#browsers)). Every automated row of the tables above passes in Safari except 1.4.10 Reflow, which is untested there: safaridriver has no BiDi `setViewport`, and Safari's window cannot go below 336 CSS pixels, so the cases at 320 CSS pixels are skipped in Safari, among them the 320 CSS pixel step of the plan page's case "the change is reached before the typed plan ID, and the page measures" (the case's other steps, and so the plan page's 1.4.3 and the change-before-ID part of 2.4.3, run and pass) and the 320 CSS pixel case of `publish.e2e.ts`. The cases "Revoke approval by keyboard" and "typing the plan ID and Approve by keyboard" pass: an earlier run failed them because safaridriver drops a repeated character from typed text ("seeded reason" arrived as "seded reason", and a random plan ID with a doubled character arrived one character short, which left Approve disabled), and the harness now sends typed text one character at a time. Three Safari differences bear on the keyboard rows, which the harness handles so that the rows test the page and not the driver. Safari's Tab skips buttons and links by default, so under Safari the harness sends Option+Tab, which reaches every control as Tab does in the other browsers; the cases do not test that a plain Tab reaches links in Safari, and a tester with the "Press Tab to highlight each item" setting on is not covered by this run. A focused select never changes under ArrowDown, Enter or Alt+ArrowDown in safaridriver, so the wizard's select rows choose in Safari by typing the option's label, which is what a keyboard user does; a form's other selects are set by value with the `input` and `change` events raised. The click on a control waits until its box has held still for 200 ms. The untested 320 CSS pixel reflow cases in Safari are an accepted limit (the lead's decision RULING-20261119-1756-38, under the owner's 2026-10-07 delegation; [client contracts §15](../docs/spec/client-contracts.md#15-assumptions-and-limits)): they run in Chrome, Edge, Firefox and Firefox ESR.

Not run here: VoiceOver (see the screen reader table below) and the previous major of Safari.

### Plan and approval screen (T8)

The plan page of T8 (W7): the change, the prediction, the typed plan ID and Approve, and Revoke
approval, from [#358](https://github.com/ginsys/bronzeward/issues/358). Automated, in
`web/e2e/auth/approval.e2e.ts` ("keyboard only (T8)"), run by `mise run browser-auth` against a
disposable deployment with synthetic data. The change shown on the plan is an injected unified diff of
synthetic text; the approval and the revocation are the server's own. Recorded on 19 November 2026
with Chrome 157, Edge 157, Firefox 160 and Firefox ESR 153 on Linux, from the `browser-auth` jobs of
[#917](https://github.com/ginsys/bronzeward/pull/917), which added the cases ([run](https://github.com/ginsys/bronzeward/actions/runs/44474115437)). The keyboard cases failed on the unchanged product
(Chrome 139, local): Approve and Revoke approval are disabled once the write is accepted, so focus fell
to the page body; the page's answer now takes it.

| Item | Screen and what is checked | chrome | edge | firefox | firefox-esr |
| --- | --- | --- | --- | --- | --- |
| 2.1.1 Keyboard, 2.1.2 No keyboard trap | the plan page by Tab alone: the change, then the typed plan ID field, then Approve by Enter; on an approved plan the reason field and Revoke approval by Enter; Tab reaches each of them and never stays on one stop on the way | pass | pass | pass | pass |
| 2.4.3 Focus order | Tab reaches the change before the typed plan ID field; after Approve and after Revoke approval, focus is in the page's answer and not on the page body | pass | pass | pass | pass |
| 4.1.3 Status messages | the approval's recorded reasons are mirrored into the page's one `role="status"` region; where focus goes after each write is in the 2.4.3 row | pass | pass | pass | pass |
| 1.4.3 Contrast | the plan page with its change: the lowest contrast of any shown text, measured in the page, at least 4.5:1 | pass | pass | pass | pass |
| 1.4.10 Reflow | at 320 CSS pixels the plan page with a long change line does not scroll sideways | pass | pass | pass | pass |

Not run here: the screen-reader half of T8, owed by [#447](https://github.com/ginsys/bronzeward/issues/447)
(see the table below), and the keyboard path of the refusal cases (a forced submit by a user without
`approver`, the 409 and 412 answers), which the same file checks by pointer and by the server's answer.

## Screen reader

| Pass | T1 | T2 | T3 (to publication) | T4 | T5 | T6 | T7 | T8 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| VoiceOver with Safari | pending: owner or designated tester | pending: owner or designated tester | owed | owed | owed | owed | owed | owed |
| NVDA with Firefox | pending: owner or designated tester | pending: owner or designated tester | owed | owed | owed | owed | owed | owed |
| NVDA with Chrome | pending: owner or designated tester | pending: owner or designated tester | owed | owed | owed | owed | owed | owed |

T3 to T8 are the tasks of [journeys §13](../docs/spec/operator-experience.md#13-usability-and-accessibility-acceptance). Their passes are owed by [#342](https://github.com/ginsys/bronzeward/issues/342), which closes on its automated acceptance, except T8's, which [#358](https://github.com/ginsys/bronzeward/issues/358) hands to [#447](https://github.com/ginsys/bronzeward/issues/447) below.

[#447](https://github.com/ginsys/bronzeward/issues/447) holds these passes and closes when they run. [#334](https://github.com/ginsys/bronzeward/issues/334) closes on its automated acceptance, by the owner's decision of 1 November 2026.
