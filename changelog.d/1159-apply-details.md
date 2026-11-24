Fixed
- The apply page and `bronzewardctl apply` leave out a machine whose waiting plan is another release's, with the reason, and send nothing for it; the apply page reads each machine's line from its position only, reads the rows again only while a machine waits or applies (after either button), and the machine page says who prepared the plan it shows. Fixes ginsys/bronzeward#1159.
