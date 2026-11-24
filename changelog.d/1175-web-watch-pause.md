Fixed
- The web console's progress watch now ends within a quarter of a second when the page stops watching, also while it waits between two attempts or between two retries of an events page read; before, those waits ran on for up to 30 seconds. A watch that ends after a dropped connection on the last events page of a finished operation now says so. Refs ginsys/bronzeward#1175.
