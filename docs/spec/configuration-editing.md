# Editing and showing configuration in the web console: structured forms and a parsed display

**Status: Proposal, not accepted by the owner.** It answers
[ginsys/bronzeward#1234](https://github.com/ginsys/bronzeward/issues/1234). It specifies no code and
changes no page. The owner accepts it, changes it or rejects it before any build item starts.

It changes the web console only. The API, the stored documents, the compiler and the command-line
client stay as they are. Where this proposal differs from an accepted text, [section 2](#2-what-this-changes-in-the-accepted-text)
says so.

## 1. What a person meets today

The facts below were read in the web client and the specifications at `origin/main`, commit `0d5375d7`.

- **A fragment is one free-text box.** The fragment editor shows a layer list, a `Document` text area
  of 16 rows, and a `Marks` text area of 3 rows (`web/src/drafts.tsx`, the fragment pane). The text is
  YAML. Save sends the layer, the document, the marks and the declarations in one write. Beside the
  boxes the editor shows two structured tables that already exist: the references in the document
  (kind, version, encoding, state) and the embedded documents (path and format).
- **A profile is not a text box.** A profile is an ordered list of pinned fragment revisions. The
  profile editor already shows it as a list of fragment names with *Move up*, *Move down*, *Remove* and an
  *Add* list (`web/src/composition.tsx`). An assignment is a list of selections in the same way.
  The owner's test words, "just a text box", fit the fragment only. This proposal changes neither list.
- **An imported configuration is a read-only text area.** *Show imported configuration* reads the stored
  document on request and shows it in a read-only text area of 24 rows, under a notice that it is a
  protected document that can hold a secret nobody has recognised yet (`web/src/claim.tsx`). The page keeps
  it in memory only and drops it when the person leaves. For a Talos template it is the whole raw text,
  with every commented example.
- **Parsing exists only in small parts.** The page already parses documents with the `yaml` package in
  two places: to find the place a refusal points at, and to list the string values of a review as
  candidates to mark as secret (`web/src/position.ts`, `web/src/marks.tsx`). Nothing shows a document as
  fields.
- **A refusal points into the text.** A refused save names a reference, a mark position or a path, and the
  editor selects that spot in the text area. A form must keep this: see [section 4](#4-the-editing-model).

**What the owner said.** In a hands-on test on 27 November 2026 the owner called the fragment and profile
boxes "just a text box fo json, no forms", asked for "better forms and displaying of structured config,
not just json boxes", and said "not parsed forms" of the imported configuration. This is the first
usability measurement of the editor. It is also the measurement that the accepted choice in section 2
said was missing.

## 2. What this changes in the accepted text

[Operator experience](operator-experience.md#12-alternatives-and-recommendations), choice C3, reads:
"Native YAML for documents and structured controls for metadata only", because "a schema form would be a
second model to keep in step with every Talos version". Its status is "accepted; usability unmeasured".
Section 6 of the same document repeats it.

This proposal asks the owner to amend C3. The amendment is the first question in
[section 8](#8-questions-for-the-owner). The text of C3 and of that sentence stays as it is in the pull
request that carries this proposal, because the owner has not accepted the change. The last build item
amends them after acceptance.

Three accepted rules bound the proposal and stay as they are:

- The configuration of a machine is native Talos configuration, and "platform metadata selects and orders
  fragments but does not redefine Talos fields" ([design §6.1](../design/Talos_Configuration_and_Machine_Management_Design.md#61-canonical-model)).
- The design lists "No proprietary Talos replacement schema": Bronzeward does not rebuild every Talos option
  behind its own model ([design §1.3](../design/Talos_Configuration_and_Machine_Management_Design.md#13-explicit-non-goals)).
- Original sanitized text keeps the editing form; the parsed form supports diffs and queries
  ([design §7.2](../design/Talos_Configuration_and_Machine_Management_Design.md#72-relational-revision-model-and-backend-contract)).

## 3. Where the structure comes from

**The Talos configuration schema, for the Talos version of the cluster being edited.**

- Talos publishes a JSON Schema of its configuration. In the pinned module
  `github.com/siderolabs/talos/pkg/machinery` at v1.14.5 it is the file `config/schemas/config.schema.json`.
  Read from the module archive on 28 November 2026: 483,234 bytes, JSON Schema draft 2020-12, 167 type
  definitions, 89 document kinds in its top-level choice, 896 descriptions and 205 lists of allowed values.
  The module generates it from its own types (a `go:generate` line in `config/config.go`) and ships it in
  the module archive. Only one of the module's tests embeds it, so the compiler does not carry it as a
  run-time asset: the web build copies the file out of the pinned module. The same file is in the upstream
  repository at the tag v1.14.5, and the two have the same SHA-256 digest.
- **Which version.** The version in use is the cluster's contract, for example `v1.14`. The cluster record
  holds it and the compiler pins it to the running minor of the nodes
  ([compilation §10.2](compilation.md#102-pinning-and-refusals)). The list of supported contracts holds
  v1.14 only. Only a contract on that list has a schema in the web build.
- **How it reaches the page.** The content policy allows no third-party fetch
  ([client contracts §10.2](client-contracts.md#102-script-and-content-policy)). So the schema is a build
  asset of the web console, generated from the pinned module in the same build as the compiler. Form,
  compiler and renderer then read one version. The page never reads the Talos documentation site.
- **A second source is needed for secrets.** The schema does not say which fields hold a secret. The
  compiler takes that from the module's `RedactSecrets` field list
  ([compilation §2.4](compilation.md#24-what-identification-does-not-promise)). The build asset therefore
  carries that list too, from the same module.
- **When the version is not known** (no contract, a contract not on the list, a missing asset, a
  document the schema cannot place): the page shows the text editor and one sentence that says why there
  is no form. It never guesses a nearby version, because a field can move between minors
  ([compilation §10.2](compilation.md#102-pinning-and-refusals), "paths are versioned with the contract").
- **A fragment revision records no contract.** The stored revision has a layer, text and declarations, and
  nothing else. The form uses the contract of the draft's cluster when the person edits. A shared fragment
  is edited inside a draft of one cluster, so that cluster's contract applies
  ([persistence and API §3.1.1](persistence-api.md#311-shared-fragments-and-profiles-the-library-scope)).
  The editor says that other clusters that adopt the fragment may run another version. This was decided
  from those rules, not left open.

## 4. The editing model

**The stored document stays the authority. The form is a view that reads and writes it.**

1. **Read.** The page parses the stored text with the `yaml` package into a document tree that records
   where each node sits in the text. A fragment is a stream of documents: each document is matched to
   a Talos document kind by its `apiVersion` and `kind`, which the schema gives as lists of allowed
   values. The legacy `v1alpha1` document has neither and is matched by its `version` key.
2. **Show.** Each matched document shows as grouped fields: a heading per object, a labelled control per
   field, the schema's description as help text, a list of allowed values as a choice list, a list as rows
   with *Move up*, *Move down* and *Remove*, a map as rows of key and value. Fields the fragment does not
   set are not shown as set; a fragment is a partial patch, so a missing "required" field is never an error.
3. **Write.** A form edit replaces the text of the node it addresses, at the place the parser recorded, or
   inserts the lines of a new key or row beside its siblings. It never writes the whole tree back, because
   printing a tree can re-indent, re-quote or re-fold text nobody touched. So the bytes outside the edit are
   unchanged by construction, and the tests of item 6 prove it. A case that cannot be written this way
   is shown as *Other settings* and is edited as text. Save sends the edited text through the existing
   write. The form sends no new request and adds no field to the body. Whether the parser's recorded
   positions are enough for every field kind is measured in build item 3, not assumed here.
4. **What the form cannot show** stays in the text and shows as an *Other settings* row with the raw lines:
   a key that is not in the schema, a choice the form cannot resolve, a `$patch` directive, an anchor or
   alias, a tag other than the reference tag, a comment, an embedded document, a value of another type than
   the schema says. The row is read-only in the form and editable in the text fallback. The form never drops,
   reorders or rewrites it.
5. **The text editor stays.** Every fragment can be opened as text, for what the form cannot hold and for a
   person who prefers it. Switching keeps the unsaved edit: the text and the form read one held document.
   The conflict view (*Their text* beside *Your text*) and *Revert to base* stay text.
6. **Round trip.** A form edit followed by Save must not drop, reorder or rewrite anything the person did not
   touch. The build checks this with tests, never by eye:
   - Open and Save with no edit sends the stored text byte for byte, on every fixture and on the Talos
     templates of each document kind.
   - One edit per field kind (text, number, yes or no, choice, list row, map row, reference) changes only the
     lines of that node. The test compares the text before and after.
   - An edit and its undo return the original bytes.
   - A document that the parser reads with an error keeps the text editor and nothing is written.
   Fixtures hold synthetic values only
   ([client contracts §11.5](client-contracts.md#115-test-artifacts)).
7. **Secrets.** A field on the secret list shows no value box. It shows a reference control: the person
   types the reference name and declares it (kind, version, encoding), as the references table does
   today. The page writes the tag `!bwref <name>` and the declaration together, in one save. The
   name grammar is the one of [compilation §5.1](compilation.md#51-syntax). A field not on the list
   but typed with a secret still goes through marks and extraction as today, and the form says that an
   unmarked value outside the list carries no promise of detection
   ([compilation §2.4](compilation.md#24-what-identification-does-not-promise)). Every example, placeholder and
   fixture of the form holds a reference where a secret belongs, never a value.
8. **Go to the problem.** A refusal that names a reference, a mark position or a path moves focus to the
   matching field, or to the *Other settings* row, or to the text when the field has no form. It uses the
   same positions as today.
9. **Marks.** A form field offers *Mark as secret*, which adds the field's path to the marks list. The marks
   box stays as the typed route.

**Profiles and assignments do not change.** They hold no Talos document: a profile pins fragment revisions
and an assignment selects names and layers. The pin list is already a list of named rows with keyboard moves,
and a Talos schema says nothing about it. A later change may show what each pinned fragment contains; that is
a use of the parsed display of section 5 and is not part of this proposal.

## 5. Showing an imported configuration as fields

- *Show imported configuration* keeps its rules: read on request, held in page memory only, dropped on
  leaving, on `pagehide` and on sign-out, no copy, download or print, the protected-document notice above it
  ([client contracts §11.4](client-contracts.md#114-web-review-and-document-input)). The parsed view is a second
  way to show the text the server already returns. It adds no route.
- The default view is the parsed fields, read-only, grouped as in section 4. Talos's commented examples are
  comments, so the default view hides them. A switch, *Show raw text*, shows the whole stored text as today,
  comments included.
- A field the schema does not know, or the view cannot place, shows under *Other settings* with its raw lines,
  as in the editor.
- Values on the secret list are already references in the stored text. They show as the reference name, never
  as a value. A value that nobody marked can still be in the stored text
  ([operator experience §9](operator-experience.md#9-secret-handling-in-the-clients)); the notice stays, and the
  parsed view makes no claim that it is clear of one.
- The fragment revision pages are outside this proposal. Their accepted display (the text in a `pre`, with its
  notice, [operator experience §12](operator-experience.md#12-alternatives-and-recommendations), choice C8)
  stays as it is; a parsed view there would amend that choice, and the owner can ask for it later.
- This proposal does not touch the draft render and per-field provenance of
  [ginsys/bronzeward#336](https://github.com/ginsys/bronzeward/issues/336). That item can use the same tree
  later. It decides nothing about it, nor about the assisted proposals of
  [ginsys/bronzeward#919](https://github.com/ginsys/bronzeward/issues/919).

## 6. Where a form sits in the console layout

This section uses the blocks of the [console layout design](../design/console-layout.md)
([ginsys/bronzeward#1235](https://github.com/ginsys/bronzeward/issues/1235)). That design is itself a
proposal the owner has not accepted. The project works with that design's recommendations for now, and the
owner can reverse them. This section follows the blocks as the design proposes them, and follows any change
the owner makes to one. It redesigns nothing in the layout: it only says where the parts of this proposal go.

- **The editor is a panel under Entries.** The layout design opens an entry's editor in its own panel
  directly under the Entries panel, and leaves the fields to this proposal (layout design, sections 2.2 and
  3.1). The form's groups from section 4 are the content of that panel, in one column.
- **The panel band** holds the panel's title (the fragment's name) and an *Edit as text* button, secondary.
  In text mode the same button reads *Edit as fields*. The layer list sits at the top of the panel body.
- **The primary action.** The page keeps one filled button, *Publish this draft*, in the page header. *Save*
  is the form's one action, at the end of the panel body, and so is drawn as a secondary (outlined) button.
  *Revert to base* is secondary beside it. *Remove* is not in the editor panel. As a destructive action it
  follows the layout design: an outlined red button, alone in a panel of its own at the end of the page, with
  one sentence above it that names the entry, as the one confirmation pattern says. The panel for
  *Discard this draft* is the same kind of panel.
- **The free-text fallback.** The *Other settings* group is the last group of the panel, under the fields. The
  text editor opens in the same panel in place of the fields, with the references table and the embedded
  documents table below it, as today. Nothing opens in a second place, and the page does not need a second column.
- **An imported configuration** stays on its page in the *Protected document* panel, opened on request by the
  one *Open* button of the draft page. The protected-document notice shows once, above the content. The parsed
  fields replace the text area as the default view, and *Show raw text* is a secondary button in the panel
  band.
- **Text width.** A field's help text, and every sentence the form adds, is at most 72 characters wide as the
  layout design requires. The fields themselves can be wider. A text area for the fallback keeps its width.
- **Order.** Tab order is the markup order: the panel band, the layer, the groups in document order, *Other
  settings*, then the actions.
- **The panel for the rendered configuration** under the editor stays as the layout design leaves it, for
  [ginsys/bronzeward#336](https://github.com/ginsys/bronzeward/issues/336).

One point the layout design may want to settle: it says a panel with a form has "the form's one action", and
that the page has one filled button. This proposal reads both together, so the form's action is outlined.

## 7. Keyboard and labels

[ginsys/bronzeward#447](https://github.com/ginsys/bronzeward/issues/447) covers the screen-reader passes. The
form keeps these rules, and each build item below keeps them:

- Every control has a visible label that is its programmatic name. A placeholder is never the label.
- A field's help text is tied to its control. Group headings are real headings, in order.
- Tab order follows document order. A list row's *Move up*, *Move down* and *Remove* are buttons with the row
  named in their label; after a move, focus stays on the moved row.
- An error is shown beside its field and in the page's one status line, in words, never by colour alone.
- A choice list is a native select; a yes-or-no field is a native checkbox.
- Autocomplete, spellcheck and capitalisation help are off on every field that can hold a value that may be a
  secret, as on the text areas today.

## 8. Questions for the owner

Each has a recommendation. Questions 1 to 4 are the owner's. Section 3 and section 4 record what was decided
on the rules above and needs no answer.

1. **Amend choice C3?** The recorded choice is "accepted; usability unmeasured", and the owner's test is
   the measurement. *Recommendation: yes, for the fragment editor and the imported configuration.*
2. **Schema-driven forms, or hand-made forms for the common fields only?** See the options below.
   *Recommendation: schema-driven.*
3. **How much of the schema?** The legacy `v1alpha1` document first, with `machine` and `cluster`, or all 89
   document kinds. *Recommendation: `v1alpha1` first, the other kinds read through the generic field rules
   and shown under Other settings where they do not fit.*
4. **What happens to a field the form cannot show?** *Recommendation: keep it, show it under Other settings,
   never delete or reorder it.*

**Decided, not asked:** parse in the page; ship the schema as a build asset; hide comments in the parsed
display by default; use the contract of the draft's cluster for a shared fragment; leave profiles and
assignments unchanged.

### Options and trade-offs

| Choice | Option | For | Against |
| --- | --- | --- | --- |
| Source of forms | **A. Schema-driven**, from the pinned schema | One source with the compiler; a new Talos minor needs a new asset, not new forms; covers every field | A generic renderer for choices, maps and lists is real work; the schema carries no secret flag, so the list is a second asset |
| | **B. Hand-made**, common fields only | Better labels and order for the few fields people change most | A second model that drifts at every Talos minor, which is what choice C3 feared; the rest stays text |
| First build | **1. Parsed read-only display first** | Proves the schema asset, the version choice and the round trip before anything writes; answers "not parsed forms" at once | The person still edits as text for a while |
| | **2. Editing first** | The editor is what the owner named first | Writes before the parser has a record |

*Recommendation: A, and display first (1).* A bounded A covers scalars, choices, lists of scalars and nested
objects. Everything else is *Other settings*.

## 9. What is not built

- No change to the API, the stored text, the compiler, marks, declarations or the command-line client.
- No form for profiles or assignments, and no change to the fragment revision pages.
- No Bronzeward schema of its own, no translation of a field between Talos versions, no automatic move of
  marks or declarations to a new contract.
- No value check beyond what the schema states; the compiler and Talos still validate at publication.
- No fix of a document that does not parse: the text editor shows it and nothing is written.
- No second contract: only the contracts on the supported list have a schema.
- No render of the compiled result or per-field provenance, and no assisted proposals.
- No claim of usability. A person must try the built forms; the usability tasks of
  [operator experience §13](operator-experience.md#13-usability-and-accessibility-acceptance) measure it.

## 10. Build items

Small pull requests, in order. Each leaves the API, the stored text, marks and the references table
unchanged. None is filed until the owner accepts this proposal.

1. **Schema asset and contract choice, no screen.** Generate the schema and the secret-field list from the
   pinned module in the web build; pick the asset by the cluster's contract; say "no form" when there is none.
   Test: the asset's digest equals the module's file.
2. **Parsed read-only display of an imported configuration**, with *Show raw text* and the hidden comments.
   Leaves the protected-document rules and the notice unchanged.
3. **Form editing of single fields in the fragment editor** (text, number, yes or no, choice) with the round-trip
   test table. Leaves the text editor, the conflict view and *Revert to base* unchanged.
4. **Lists, maps and nested objects**, with row moves. Leaves everything in item 3 unchanged.
5. **The reference control for secret fields**, with the declaration written in the same save, and
   *Mark as secret* on a field.
6. **Go to the problem** from a refusal into a field. Leaves the text positions unchanged.
7. **Amend choice C3** and the sentence of operator experience section 6, after the owner accepts.
