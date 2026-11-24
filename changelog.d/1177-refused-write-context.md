Fixed
- A CLI write that is refused before it is sent (a repeat for another principal or epoch, a write whose reads are
  of another epoch, a refusal of the server's start mode or revision) no longer prints `sending <route>.` first. The
  line now comes just before the first attempt, so it appears only for a write that is sent. Fixes ginsys/bronzeward#1177.
