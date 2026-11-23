# Quick start: the web interface

This page takes you from a browser to your first read of a running installation: who you are, what
the server is, and which machines it knows. It writes nothing. It assumes one person who holds every
role they need, and a server and web files that your administrator already set up
([hosting](../deployment/hosting.md), [identity provider](../deployment/identity-provider.md)).

You need the web address of the installation, for example `https://bronzeward.example.com`, and an
account at the identity provider. Use a current browser. The page needs JavaScript.

The steps are numbered. Each one names what you do and what you should see.

1. **Open the address.** Type the web address in the browser. The page loads, shows "Loading" for a
   moment, and then shows a button: **Sign in with the identity provider**. If instead it shows a
   message and no button, read it: the page puts the reason on the screen. An open page does not
   show that the server is well, only that it answered.
2. **Sign in.** Press the button. The browser moves to the identity provider. Sign in there. The
   identity provider sends the browser back to the installation.
3. **Read who you are.** The header shows "Signed in as" and your name, then "Roles:" and the list
   of your roles. This is what the **server** sees, not what you hoped for. If a role you need is
   missing, ask your administrator. Pressing the button again does not add roles.
4. **Read the machines.** The first page is **Machines**. It shows the machines the server
   returned to you just now. If it says "No machines recorded yet.", the installation has none
   recorded. It does not show that a machine is reachable, or that it runs the configuration it
   should.
5. **Look around.** The left navigation has three groups. Inventory holds Machines, Clusters and
   Fragments and profiles. Delivery holds Drafts and releases, Plans and Operations. Governance
   holds Audit and, for a recovery administrator, Recovery. Press a name to open it. This page
   only asks you to read: stop at any page that shows a button for a change.
6. **Read the versions.** At the bottom of every page the footer reads "Server" with the server's
   version, then "API revision" and its number. The page needs a minimum API revision. A server
   below it is not used: the page says "This server serves API revision N; this client needs API
   revision M or later." instead of showing pages.
7. **Sign out.** Press **Sign out** in the header. The page says "You are signed out of
   Bronzeward." and offers **Sign in** again. If a form holds text you have not saved, the page asks before it drops it.

## What you did, and what you did not

You signed in and read the pages you opened. You asked for no change on the server or on any
machine. A list that shows a machine does not show that the machine runs what it should: the
drift page does that, in the journey pages.

## If a step fails

- Step 1 shows a certificate warning from the browser: the installation uses a certificate your
  browser does not trust. Ask your administrator for the authority's certificate and add it to the
  browser or the system, as for any website. The page cannot fix that for you.
- Step 2 returns to a page that says "Not accepted by this installation. Sign in again.": the
  server did not accept the sign-in. Press **Sign in again**. If it repeats, give your administrator the
  time and the text of the message.
