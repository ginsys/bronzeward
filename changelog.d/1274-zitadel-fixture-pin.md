Changed
- The disposable test identity provider and its login page now run ZITADEL v4.20.6 (both images pinned by digest) instead of v4.19.5, which carries the upstream login flaw that let a first-factor session enrol an authenticator without the second factor. This changes the test fixtures only. Refs ginsys/bronzeward#1274.
