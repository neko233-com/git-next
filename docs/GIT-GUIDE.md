# The Git guide

This is the manual to read instead of the official Git documentation. The official
reference is complete, and that is exactly the problem: it is organised as a
reference for people who already know Git, and it describes every option of every
command with the same weight. This guide is organised as a path. Read it from the
top, run the commands as you reach them, and you will end up knowing the part of
Git that daily work needs.

Every command here is one this build accepts. If a command is not in this guide, it
is either not needed for the task at hand or not implemented yet - [the last
section](#what-this-build-does-not-do-yet) says which, plainly.

`git` in the examples is the installed git-next entry point. Before takeover, run
the same commands with `git-next`.

## Contents

1. [The five ideas](#1-the-five-ideas)
2. [Set your name once](#2-set-your-name-once)
3. [Your first repository](#3-your-first-repository)
4. [Looking at what you have](#4-looking-at-what-you-have)
5. [Undoing things](#5-undoing-things)
6. [Branches](#6-branches)
7. [Sharing work](#7-sharing-work)
8. [Line endings](#8-line-endings)
9. [Large files](#9-large-files)
10. [Scripting Git](#10-scripting-git)
11. [When something goes wrong](#11-when-something-goes-wrong)
12. [Cheat sheet](#12-cheat-sheet)
13. [What this build does not do yet](#what-this-build-does-not-do-yet)

## 1. The five ideas

Git has a reputation for being hard. The commands are not the hard part; five ideas
are, and they fit in one page.

**A commit is a snapshot, not a diff.** When you commit, Git records the entire
state of your files at that moment and gives it an identifier. The identifier is a
hash such as `4f2a9c1`, derived from the content, so the same content always
produces the same identifier. Two commits that differ by one character are
different commits.

**A repository is a box of snapshots.** It lives in the `.git` directory inside your
project. Your visible files are the *working tree*; `.git` holds every snapshot you
have ever committed plus the bookkeeping to move between them. Copy the folder and
you copy the whole history.

**The index is the shopping basket.** Between your working tree and the next commit
sits the *index* (also called the *staging area*). `git add` puts a file into the
basket; `git commit` turns the basket into a snapshot. This extra step exists so you
can commit three of your five changed files and leave two for later.

**A branch is a movable label on one snapshot.** `main` is not a folder and not a
copy of your files. It is a name pointing at a commit. Committing moves the label
forward. Creating a branch is therefore instant and costs nothing, which is why Git
asks you to branch for everything.

**A remote is another box, with a name.** `origin` is the conventional name of the
box you cloned from. Your repository keeps its own labels for the snapshots the
remote had when you last fetched. `git fetch` updates those labels; merging them into
your branch is a separate step, and `git pull` is the two steps together.

If those five make sense, the rest of this guide is vocabulary.

## 2. Set your name once

Every commit records who made it. Set that once, for your user account:

```sh
git config --global user.name "Your Name"
git config --global user.email "you@example.com"
```

Check what Git thinks:

```sh
git config --list
git config --get user.email
```

`--global` writes to your user configuration and applies to every repository.
Without it, the setting applies to the current repository only. The other scopes are
`--local` (the same as the default), `--system`, `--worktree`, and `--file <path>`
for an explicit file.

New repositories default to the branch name `main`. If your team uses another name,
make it the default in your user configuration rather than typing it every time:

```sh
git config --global init.defaultBranch main
```

## 3. Your first repository

Create a project and make the first snapshot:

```sh
git init my-project
cd my-project
git status
```

`git init` prints the directory it initialised and creates two small files: a
`.gitattributes` and a `.editorconfig` that declare the line-ending policy for the
new repository. Commit them as the first commit, so the whole team shares the same
policy. `git status` is the command you will run most often; it answers "where am I,
and what is not yet recorded".

Write something, then look again:

```sh
echo "hello" > notes.txt
git status
```

The file is listed as untracked. Git sees it and deliberately does not record it
until you ask. Put it in the basket:

```sh
git add notes.txt
git status
```

Now it is listed under changes to be committed. Record it:

```sh
git commit -m "Add notes"
```

The message is the sentence your future self will read. Write what changed and why:
`Fix login redirect for expired sessions` beats `update`.

Two shortcuts are worth knowing:

```sh
git add -A                  # put every change in the basket
git commit -m "Message"     # record the basket
```

And to see what you have recorded:

```sh
git log --oneline
```

Each line is one commit: a short identifier and the message. Use `git log` without
`--oneline` for the full form, `-n 5` to limit the count, and `--format` when you
want specific fields.

## 4. Looking at what you have

Three questions, three commands.

**What is the state of everything?** `git status` groups changes by where they are:
staged (in the basket), not staged (changed but not added), untracked (never added),
and unmerged (conflicted). The useful variants:

```sh
git status --short          # one line per file, no prose
git status -b               # include the branch and its upstream state
git status --ahead-behind   # include how far ahead or behind the upstream you are
git status -u all           # list every untracked file, not just the directories
```

**What exactly changed?** `git diff` compares two states and prints the difference:

```sh
git diff                    # working tree against the basket
git diff --cached           # basket against the last commit
git diff HEAD               # working tree against the last commit
git diff main feature       # two branches
git diff --stat             # one line per file instead of full content
git diff --name-only        # just the file names
git diff -- path/to/file    # limit to a path
```

In the output, `-` lines were removed and `+` lines were added. The `@@` header
tells you which lines of which file you are looking at.

**What was in that commit?** `git show` prints one commit and its changes:

```sh
git show                    # the last commit
git show 4f2a9c1            # a specific commit
git show --stat 4f2a9c1     # summary only
```

To read a file as it was in some commit:

```sh
git show 4f2a9c1:notes.txt
```

## 5. Undoing things

Almost every "help, I broke it" question is one of five cases. Pick the one that
matches, and note that the first three are safe: they cannot lose work that was
already committed.

**Discard changes you have not added yet.** This throws away working-tree edits, so
be sure the file is the one you mean:

```sh
git restore notes.txt
git restore .                       # the whole directory
```

**Take a file back out of the basket**, keeping your edits:

```sh
git restore --staged notes.txt
```

**Undo the last commit but keep its changes** in the basket, ready to commit again
with a better message:

```sh
git reset --soft HEAD~1
```

`HEAD~1` means "the commit before the current one". `--mixed` (the default) also
unstages the changes; `--hard` discards them, which is the only form that loses
data:

```sh
git reset HEAD~1            # undo the commit, keep the changes unstaged
git reset --hard HEAD       # discard every uncommitted change
```

**Restore a file from an older commit**, without moving the branch:

```sh
git restore --source HEAD~1 notes.txt
```

**Return one file to a specific version and stage it**, which is how you recover a
file you deleted and committed by accident:

```sh
git restore --source 4f2a9c1 notes.txt
git add notes.txt
git commit -m "Restore notes.txt"
```

`git reset --hard` prints no confirmation and cannot be undone from the working
tree. If you are unsure, commit first: a commit is cheap, and it is the only thing
that makes the next step reversible.

## 6. Branches

A branch lets you try something without disturbing `main`. Create one and switch to
it in a single step:

```sh
git switch -c fix-login
```

Do the work, commit as usual. `git status` now says which branch you are on. When the
work is ready, put it back into `main`:

```sh
git switch main
git merge fix-login
```

If `main` has not moved since you branched, the merge is a *fast-forward*: `main`
simply moves forward to your commit, and Git says so. If both branches have new
commits, Git creates a *merge commit* with two parents.

Useful forms:

```sh
git branch                     # list local branches, marking the current one
git merge --ff-only fix-login  # refuse unless the merge is a pure fast-forward
git merge --no-ff fix-login    # always create a merge commit, even when it could fast-forward
git merge --no-commit fix-login # merge but leave the result uncommitted for review
```

### Conflicts

A conflict happens when both branches changed the same lines. Git stops and marks
the file:

```
<<<<<<< HEAD
the version on the branch you are on
=======
the version from the branch you are merging
>>>>>>> fix-login
```

Edit the file until it reads the way you want, with no marker lines left. Then:

```sh
git add the-conflicted-file
git commit                 # concludes the merge, no -m needed
```

To give up and go back to exactly where you started:

```sh
git merge --abort
```

`git status` prints the reminder for both cases while a merge is in progress, and
`git merge --continue` resumes after you have resolved everything.

## 7. Sharing work

**Start from an existing repository.** This copies the history and checks out the
default branch:

```sh
git clone https://example.com/team/project.git
git clone https://example.com/team/project.git my-folder
git clone --depth 1 https://example.com/team/project.git   # recent history only
git clone -b release/1.0 https://example.com/team/project.git
```

**Connect an existing project to a remote:**

```sh
git remote add origin https://example.com/team/project.git
git remote -v              # show the URLs
git remote show             # show the configured remotes
git remote get-url origin
```

**Get other people's work without changing yours:**

```sh
git fetch
```

This downloads new commits and updates the remote-tracking labels. Your files and
your branches do not move. Nothing is merged, so nothing can break.

**Bring those changes into your branch:**

```sh
git pull --ff-only
```

`--ff-only` means "only if my branch can simply move forward". That is the safe
default: if your branch has its own commits and the remote has others, the pull
refuses instead of inventing a merge you did not ask for. Merge explicitly when that
is what you want:

```sh
git fetch
git merge origin/main
```

**Publish your commits:**

```sh
git push -u origin main     # the first time: also record the upstream
git push                    # afterwards
git push --dry-run          # show what would be sent
git push origin my-branch
git push --tags             # send tags as well
```

`-u` records which remote branch yours tracks, which is what makes the plain
`git push` and `git status -b` work afterwards. A push is refused when it would
overwrite commits you have not seen; that refusal is a feature, and the fix is to
fetch and merge, not to force. `--force-with-lease` exists for the rare case where
you rewrote your own unpublished commits, and it refuses if someone else pushed in
the meantime.

## 8. Line endings

Windows and Linux disagree about the end of a line: Windows tools write `CRLF`,
Linux tools write `LF`. A repository that mixes them shows every file as modified
after a checkout on the other platform. This build defaults new repositories to LF
and can report the policy in force:

```sh
git eol                     # report the policy
git eol --json              # the same, machine readable
git eol --apply             # apply the policy to the repository
git add --renormalize .     # re-record tracked files under the current policy
```

The policy is shared with EditorConfig, so a `.editorconfig` in the repository can
declare exceptions. Binary files and explicitly excluded paths are left byte for
byte. Existing tracked content is never renormalised behind your back: `--apply` and
`--renormalize` are explicit, and they are a commit like any other.

## 9. Large files

Git stores every version of every file, which is the wrong tool for a 500 MB video.
Git LFS keeps a small pointer in the repository and the content beside it:

```sh
git lfs install             # enable LFS for this repository
git lfs track "*.psd"       # record the pattern in .gitattributes
git add .gitattributes
git add design.psd
git commit -m "Track design files with LFS"
```

`git lfs track` writes a rule; commit that rule, or your teammates will not share it.
`git lfs version` reports the implementation, and `git lfs pointer --file <path>`
shows the small file that stands in for the content.

## 10. Scripting Git

Human output is for humans. When a program reads the result, ask for a stable form.

```sh
git status --porcelain          # stable, one line per file
git status --porcelain=v2       # stable, more detail
git status -z                   # NUL separated, safe for any filename
git diff --name-status          # status letter plus path
git diff --numstat              # added and removed counts per file
git log --format=%H --max-count=1
git rev-parse HEAD              # the full identifier of the current commit
git rev-parse HEAD~1            # any revision expression
git rev-parse --show-toplevel   # the root of the working tree
git rev-parse --git-dir         # where the repository itself lives
git status --porcelain=v2 -b    # `# branch.head` names the current branch
git ls-files                    # what is in the index
git show-ref                    # every ref and its commit
git cat-file -p HEAD            # the raw commit object
git hash-object -w notes.txt    # store a file as an object, print its identifier
git ls-remote origin            # what the remote has, without fetching
```

These formats are stable across interface languages, so a script does not break when
a user switches language. Human output is translated; machine output is not.

## 11. When something goes wrong

| Message | What it means | What to do |
| --- | --- | --- |
| `not a Git repository` | You are outside a project, or above the directory holding `.git` | `cd` into the project, or `git -C <path> status` |
| `nothing to commit, working tree clean` | There is nothing new; this is success, not an error | Nothing |
| `no changes added to commit` | Files changed but none were added | `git add -A`, then commit |
| `You have unmerged paths` | A merge stopped on a conflict | Edit the files, `git add` them, `git commit` |
| `Your branch is behind ... and can be fast-forwarded` | The remote has commits you do not | `git pull --ff-only` |
| `Your branch and 'origin/main' have diverged` | Both sides have new commits | `git merge origin/main`, resolve, commit |
| `! [rejected] ... (non-fast-forward)` | The push would overwrite commits you have not seen | `git fetch`, merge, push again |
| `error: Your local changes would be overwritten` | Switching would destroy uncommitted work | Commit or `git restore` the files first |
| `fatal: destination already contains a repository` | `git init` found an existing repository | Use the existing one, or choose another directory |
| `Another git process seems to be running` | A previous command is still holding the index lock | Wait, or remove `.git/index.lock` if nothing is running |

Two habits prevent most of these: run `git status` before you switch branches, and
commit before you try anything you are unsure about.

## 12. Cheat sheet

```sh
# start
git init                      # new repository here
git clone <url>               # copy an existing one
git config --global user.name "Your Name"
git config --global user.email "you@example.com"

# daily
git status                    # what changed and where
git diff                      # exactly what changed
git add -A                    # put everything in the basket
git commit -m "Message"       # record it
git log --oneline             # what has been recorded

# branches
git switch -c topic           # new branch and switch to it
git switch main               # back to main
git merge topic               # bring topic into the current branch
git branch                    # list branches

# remotes
git fetch                     # download, change nothing
git pull --ff-only            # fast-forward only
git push -u origin main       # publish and record the upstream

# undo
git restore <file>            # discard uncommitted edits
git restore --staged <file>   # unstage, keep the edits
git reset --soft HEAD~1       # undo the commit, keep the changes
git reset --hard HEAD         # discard everything uncommitted

# large files
git lfs install
git lfs track "*.bin"
```

## What this build does not do yet

Honesty is part of a manual. This build does not implement the whole Git command
surface, and it refuses rather than pretending:

- **No interactive rebase, stash, cherry-pick or revert.** Editing history in place
  is not available; write a new commit or use `git reset --soft` to redo the last one.
- **`git pull` requires `--ff-only`.** A pull that would need a merge refuses; run
  `git fetch` then `git merge` yourself.
- **`git checkout` and `git switch` need a clean ordinary-file worktree.** Staged or
  unstaged changes, symlinks, submodules and sparse indexes cause a clear refusal
  instead of a partial checkout.
- **Push covers the common cases, not every refspec form.** Wildcard push refspecs,
  multiple push URLs and the receive-pack server command are not implemented.
- **LFS transport and locking are partial.** Pointer handling and the local workflow
  work; the full transport and lock protocol do not.
- **Hooks, signing and submodules are not executed or managed yet.**

`git doctor` prints the current list of blockers for replacing Git on this machine,
and `git compatibility` prints the compatibility contract. Both are generated from
the same report the guard uses, so they cannot drift from the behaviour. See
[why git-next](WHY-GIT-NEXT.md) for what the build does beyond the Git command set,
and [delivery status](DELIVERY_STATUS.md) for the measured scope.
