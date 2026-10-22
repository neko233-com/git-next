# git-next pathspec integration patch

Upstream: gix-pathspec 0.22.0, MIT OR Apache-2.0; retain upstream license files.

A normalized root `.` is an always-matching pattern with a display path of `.`. Its common-prefix optimization must be empty; using the display byte as a required prefix silently excludes ordinary paths. `search/init.rs` now treats every always-matching positive pattern as a zero-length common prefix. Other patterns and exclusion ordering are unchanged.

The disposable diff and restore/reset acceptance fixtures compare root `.`, subdirectory `.`, glob and exclusion behavior with reference Git. The client itself never dispatches to Git.
