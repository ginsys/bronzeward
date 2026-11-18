# Web accessibility record

The WCAG 2.2 AA items of [journeys §13](../docs/spec/operator-experience.md#13-usability-and-accessibility-acceptance)
on the views of [#334](https://github.com/ginsys/bronzeward/issues/334), for usability tasks T1
(sign in, say which roles you hold) and T2 (find the drifted machine and state its desired,
applied and observed release and observation time), per browser. Coverage is the one the owner
accepted in [#255](https://github.com/ginsys/bronzeward/issues/255): keyboard on every tested
browser; screen reader on VoiceOver with Safari, and on NVDA with Firefox and with Chrome.

An automated check is evidence for what it measures, not conformance (§13). Items whose views
come with later tickets (approval, the plan evidence's focus order, reflow of the diff in review)
are recorded by those tickets. The import wizard, the protected import review and the conflict
view of the draft editor are recorded in the last section of the keyboard and measured checks.

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
| 1.4.11 Non-text contrast | focus outline `#1a4fc4` on white 6.9:1; table rules `#767676` on white 4.5:1 | by computation | | | |
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
| 1.4.3 Contrast, 1.4.11 Non-text contrast | the lowest contrast of any shown text, redaction badges included, is at least 4.5:1; the badge border is `#1a4fc4` on white, 6.9:1 |
| 1.4.1 Use of colour | a redaction token shows as `[secret registry/example-pass, version 1]`: its kind is words inside a border, never colour alone |
| 1.4.10 Reflow | at 320 CSS pixels neither page scrolls sideways; the provenance table scrolls in its own labelled region |
| 2.5.8 Target size | every button, select and navigation link at least 24 by 24 px |

### Import wizard, protected review and conflict view

The tasks T3 (the import wizard, the import page and the protected review) and T5 (the conflict view of
the draft editor), from [#894](https://github.com/ginsys/bronzeward/issues/894). Automated, in
`web/e2e/auth/a11y-import.e2e.ts`, run by `mise run browser-auth` against a disposable deployment
with synthetic data. Recorded on 18 November 2026 with Chrome 157, Edge 157, Firefox 160 and Firefox
ESR 153 on Linux, from the `browser-auth` jobs of the pull request that added the cases
([#903](https://github.com/ginsys/bronzeward/pull/903)). A failure names its issue.
The cases keep nothing protected: the storage check, the HTTP cache check and the artifact scan hold
after each case.

| Item | Screen and what is checked | chrome | edge | firefox | firefox-esr |
| --- | --- | --- | --- | --- | --- |
| 2.1.1 Keyboard, 2.1.2 No keyboard trap | wizard: the Changes and Imports links, Start import, the machine and draft selects and the source and staging radios by arrow keys, a typed document, Start import; Tab leaves every control | pass | pass | pass | pass |
| 2.1.1 Keyboard, 2.1.2 No keyboard trap | protected review: Review import, a Mark as secret button, Send marks, Continue import, and Abandon import by Enter | pass | pass | pass | pass |
| 2.1.1 Keyboard, 2.1.2 No keyboard trap | conflict view: the entry, Show their change and Copy my text by Tab and Enter; Save by Enter once focused | pass | pass | pass | pass |
| 2.1.1 Keyboard | conflict view: a Tab stop on Save in the conflict state (not confirmed: the stop order differed between runs) | fail, [#896](https://github.com/ginsys/bronzeward/issues/896) (Chrome 139, local) | not run | not run | not run |
| 2.4.7 Focus visible | wizard, protected review, conflict view: every focus Tab reaches shows an outline of at least 2 px | pass | pass | pass | pass |
| 2.4.3 Focus order | wizard: a page change moves focus to the new page, and Tab reaches the named controls from there | pass | pass | pass | pass |
| 2.4.3 Focus order | protected review: focus after Review import, Send marks, Continue import and Abandon import (it drops to the page body) | fail, [#896](https://github.com/ginsys/bronzeward/issues/896) (Chrome 139, local) | not run | not run | not run |
| 2.4.3 Focus order | conflict view: focus after a refused save, Copy my text and the saved text (it drops to the page body); Show their change keeps focus | fail, [#896](https://github.com/ginsys/bronzeward/issues/896) (Chrome 139, local) | not run | not run | not run |
| 4.1.3 Status messages | wizard: the refused start and the released import; protected review: the open review; conflict view: the refused save; each shown in a `role="status"` region or an alert, and the wizard's released-import page holds exactly one such region. That focus stays put after each is part of the 2.4.3 rows | pass | pass | pass | pass |
| 1.4.3 Contrast | wizard, protected review, conflict view: the lowest contrast of any shown text, measured in the page, at least 4.5:1 | pass | pass | pass | pass |
| 1.4.10 Reflow | at 320 CSS pixels no page scrolls sideways: the import wizard with a long draft title, the import page, the paused import, its open review and marks panel, and the conflict view with their text and mine | pass | pass | pass | pass |
| 2.5.8 Target size | wizard, protected review, conflict view: every button, select and navigation link at least 24 by 24 px | pass | pass | pass | pass |
| 3.3.1 Error identification | wizard: a start with no document is refused in text beside the button, "Not sent: paste the document or choose its file." | pass | pass | pass | pass |

The three "fail" rows are product defects found by local runs on Chrome 139 and tracked in
[#896](https://github.com/ginsys/bronzeward/issues/896); the cases leave those assertions out
until that issue fixes them, so no other browser has run them and the cells read "not run". The publish screen of T3 is not run here:
[#897](https://github.com/ginsys/bronzeward/issues/897).

Not run here: Safari, whose WebDriver needs a macOS runner; previous major versions (B6).

## Screen reader

| Pass | T1 | T2 | T3 (to publication) | T4 | T5 | T6 | T7 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| VoiceOver with Safari | pending: owner or designated tester | pending: owner or designated tester | owed | owed | owed | owed | owed |
| NVDA with Firefox | pending: owner or designated tester | pending: owner or designated tester | owed | owed | owed | owed | owed |
| NVDA with Chrome | pending: owner or designated tester | pending: owner or designated tester | owed | owed | owed | owed | owed |

T3 to T7 are the tasks of [journeys §13](../docs/spec/operator-experience.md#13-usability-and-accessibility-acceptance). Their passes are owed by [#342](https://github.com/ginsys/bronzeward/issues/342), which closes on its automated acceptance.

[#447](https://github.com/ginsys/bronzeward/issues/447) holds these passes and closes when they run. [#334](https://github.com/ginsys/bronzeward/issues/334) closes on its automated acceptance, by the owner's decision of 1 November 2026.
