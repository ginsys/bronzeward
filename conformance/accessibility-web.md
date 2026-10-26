# Web accessibility record

The WCAG 2.2 AA items of [journeys §13](../docs/spec/operator-experience.md#13-usability-and-accessibility-acceptance)
on the views of [#334](https://github.com/ginsys/bronzeward/issues/334), for usability tasks T1
(sign in, say which roles you hold) and T2 (find the drifted machine and state its desired,
applied and observed release and observation time), per browser. Coverage is the one the owner
accepted in [#255](https://github.com/ginsys/bronzeward/issues/255): keyboard on every tested
browser; screen reader on VoiceOver with Safari, and on NVDA with Firefox and with Chrome.

An automated check is evidence for what it measures, not conformance (§13). Items whose views
come with later tickets (the YAML editor, approval, the plan evidence's focus order, errors at a
field, redaction badges, reflow of the diff in review) are recorded by those tickets.

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
| 4.1.3 Status messages | every state text is a `role="status"` element, announced without moving focus; the empty machine list checks the role | pass | pass | pass | pass |
| 1.4.3 Contrast | the lowest contrast of any shown text, measured in the page, at least 4.5:1 | pass | pass | pass | pass |
| 1.4.11 Non-text contrast | focus outline `#1a4fc4` on white 6.9:1; table rules `#767676` on white 4.5:1 | by computation | | | |
| 1.4.10 Reflow | at 320 CSS pixels no page scrolls sideways: machines, plans and a plan with its diff, sources, a fragment and its revision's document, operations and an operation; a table scrolls in its own labelled, focusable region | pass | pass | pass | pass |
| 2.5.8 Target size | every button, select and navigation link at least 24 by 24 px | pass | pass | pass | pass |
| 1.4.1 Use of colour | drift, freeze, staleness and failure are words; the views set no colour with meaning | by construction; the words in `web/src/*.test.ts` | | | |
| 3.3.1 Error identification | these views have no input field | not applicable | | | |

Not run here: Safari, whose WebDriver needs a macOS runner; previous major versions (B6).

## Screen reader

| Pass | T1 | T2 |
| --- | --- | --- |
| VoiceOver with Safari | pending: owner or designated tester | pending: owner or designated tester |
| NVDA with Firefox | pending: owner or designated tester | pending: owner or designated tester |
| NVDA with Chrome | pending: owner or designated tester | pending: owner or designated tester |

[#334](https://github.com/ginsys/bronzeward/issues/334) stays open until these passes run.
