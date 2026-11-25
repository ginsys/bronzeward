# Quick start: the web interface

This page takes you from a browser to your first read of a running installation: who you are, what
the server is, and which machines it knows. It writes nothing. It assumes one person who holds every
role they need, and a server and web files that your administrator already set up
([hosting](../deployment/hosting.md), [identity provider](../deployment/identity-provider.md)).

You need the web address of the installation, for example `https://bronzeward.example.com`, and an
account at the identity provider. Use a current browser. The page needs JavaScript.

The steps are numbered. Each one names what you do and what you should see.

<!-- bw-web-steps: quick-start-web -->

1. **Open the address.** Type the web address in the browser. The page loads and shows "Loading"
   for a moment. With no sign-in open in another tab of this browser, the page then sends the
   browser to the identity provider by itself, with no button to press. If it shows a message
   instead, read it: the page puts the reason on the screen. An open page does not show that the
   server is well, only that it answered.
2. **Sign in.** Sign in at the identity provider. It sends the browser back to the installation.
   If the sign-in fails, the page shows the reason and a button, **Sign in with the identity
   provider**, to start again.
3. **Read who you are.** The header shows "Signed in as" and then "Roles:" and the list of your
   roles. The roles are what the **server** sees, not what you hoped for. If a role you need is
   missing, ask your administrator. Signing in again does not add roles. The name after "Signed in
   as" comes from your identity provider, never from the server. If the provider gives no name,
   the header reads "Signed in as a person".
4. **Read the machines.** The first page is **Machines**. It shows the machines the server
   returned to you just now. If it says "No machines recorded yet.", the installation has none
   recorded. It does not show that a machine is reachable, or that it runs the configuration it
   should.

   ![The Machines page of the web client, as a viewer sees it after sign-in. The header reads
   "Signed in as h-viewer synthetic. Roles: viewer." The navigation on the left shows Inventory,
   Delivery and Governance. The table lists two machines, 10.55.0.3 and 10.55.0.2, each with
   Desired and Applied "S1 import", Observed "= applied, 7 min ago" and State "Release not
   applied yet". The footer reads "Client (devel). Server (devel), API revision 17". Taken on the
   participant fixture at revision 454b5609, with synthetic names.](images/quick-start-web-1.png)

5. **Look around.** The left navigation has three groups. Inventory holds Machines, Clusters and
   Fragments and profiles. Delivery holds Drafts and releases, Plans and Operations. Governance
   holds Audit and, for a recovery administrator or while the installation is in recovery mode,
   Recovery. Seeing a link does not mean you may change anything there. Press a name to open it.
   This page only asks you to read: stop at any page that shows a button for a change.
6. **Read the versions.** At the bottom of every page the footer reads "Server" with the server's
   version, then "API revision" and its number. The page needs a minimum API revision. A server
   below it is not used: the page says "This server serves API revision N; this client needs API
   revision M or later." instead of showing pages.
7. **Sign out.** Press **Sign out** in the header. The page says "You are signed out of
   Bronzeward." and a **Sign in** link. If a form holds text you have not saved, the page asks
   before it drops it.

## What you did, and what you did not

This page is written from the web client's source and its specification. One part was run in a
browser: a sign-in on the participant fixture at revision 454b5609, then a read of Machines. The
screenshot in step 4 shows the result: the header after sign-in (step 3), the Machines table, the
navigation (step 5) and the footer (step 6). It does not show a sign-in failure, the too-old
message of step 6 or the sign-out of step 7: none of those was run in a browser, nor was either
failure case below. You signed in and read the pages you opened. You asked for no change on the
server or on any machine. A list that shows a machine does not show that the machine runs what it
should: the drift page does that, in the journey pages.

## If a step fails

- Step 1 shows a certificate warning from the browser: the installation uses a certificate your
  browser does not trust. Ask your administrator for the authority's certificate and add it to the
  browser or the system, as for any website. The page cannot fix that for you.
- Step 2 returns to a page that says "Not accepted by this installation. Sign in again.": the
  server did not accept the sign-in. Press **Sign in with the identity provider**. If it repeats,
  give your administrator the time and the text of the message.

## Next

- To stay signed in and to read a refusal: [sign in and renewal](sign-in-and-renewal.md).
- To read the machines and clusters in detail: [the inventory](inventory.md).
- For the same first read on the command line: [quick start for the command line](quick-start-cli.md).
- For a word you do not know, such as a role: [the word list](terms.md).
