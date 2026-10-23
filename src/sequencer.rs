use anyhow::{Context, Result, bail, ensure};
use clap::Parser;
use gix::{
    bstr::ByteSlice,
    refs::transaction::{PreviousValue, RefEdit},
};

#[derive(Parser, Debug)]
pub struct CherryPickOptions {
    #[arg(long, group = "sequencer_action")]
    abort: bool,
    #[arg(long = "continue", group = "sequencer_action")]
    continue_pick: bool,
    #[arg(long)]
    no_commit: bool,
    revision: Option<String>,
}

#[derive(Parser, Debug)]
pub struct RevertOptions {
    #[arg(long)]
    no_commit: bool,
    revision: String,
}

#[derive(Parser, Debug)]
pub struct RebaseOptions {
    #[arg(long, group = "rebase_action")]
    abort: bool,
    #[arg(long = "continue", group = "rebase_action")]
    continue_rebase: bool,
    #[arg(long = "onto")]
    onto: Option<String>,
    upstream: Option<String>,
    branch: Option<String>,
}

pub fn cherry_pick(options: CherryPickOptions) -> Result<()> {
    if options.abort {
        return abort_marker("CHERRY_PICK_HEAD");
    }
    if options.continue_pick {
        bail!("cherry-pick --continue requires a pending cherry-pick with conflicts");
    }
    let revision = options
        .revision
        .context("cherry-pick requires a commit revision")?;
    apply_revision(&revision, false, options.no_commit)
}

pub fn revert(options: RevertOptions) -> Result<()> {
    apply_revision(&options.revision, true, options.no_commit)
}

pub fn rebase(options: RebaseOptions) -> Result<()> {
    if options.abort {
        return abort_marker("REBASE_HEAD");
    }
    if options.continue_rebase {
        bail!("rebase --continue requires a pending rebase");
    }
    let repo = crate::repository::open()?;
    let upstream_name = options
        .upstream
        .as_deref()
        .context("rebase requires an upstream branch or commit")?;
    let onto_name = options.onto.as_deref().unwrap_or(upstream_name);
    let branch_name = options.branch.as_deref().unwrap_or("HEAD");
    let onto = repo
        .rev_parse_single(onto_name.as_bytes().as_bstr())?
        .object()?
        .peel_to_commit()?
        .id;
    let upstream = repo
        .rev_parse_single(upstream_name.as_bytes().as_bstr())?
        .object()?
        .peel_to_commit()?
        .id;
    let branch = repo
        .rev_parse_single(branch_name.as_bytes().as_bstr())?
        .object()?
        .peel_to_commit()?
        .id;
    let mut commits = Vec::new();
    let mut current = branch;
    while current != upstream && !crate::worktree::is_ancestor(&repo, current, upstream)? {
        commits.push(current);
        let commit = repo.find_commit(current)?;
        let parents = commit.parent_ids().collect::<Vec<_>>();
        ensure!(parents.len() == 1, "rebase refuses merge commits");
        current = parents[0].detach();
    }
    commits.reverse();
    ensure!(!commits.is_empty(), "current branch is up to date");
    crate::worktree::switch(Some(&onto.to_string()), None, true)?;
    for commit in commits {
        apply_revision(&commit.to_string(), false, false)?;
    }
    Ok(())
}

fn abort_marker(name: &str) -> Result<()> {
    let repo = crate::repository::open()?;
    let path = repo.git_dir().join(name);
    ensure!(path.is_file(), "no cherry-pick or rebase in progress");
    std::fs::remove_file(path)?;
    Ok(())
}

fn apply_revision(revision: &str, revert: bool, no_commit: bool) -> Result<()> {
    let repo = crate::lfs::prepare(crate::repository::open()?)?;
    crate::write::working_tree(&repo)?;
    crate::write::safe_sequencer_state(&repo)?;
    let head = repo.head_id()?.detach();
    let expected_head: gix::refs::Target = repo.find_reference("HEAD")?.target().into();
    let object = repo
        .rev_parse_single(revision.as_bytes().as_bstr())?
        .object()?;
    let picked = object.peel_to_commit()?.id;
    let parents = repo.find_commit(picked)?.parent_ids().collect::<Vec<_>>();
    ensure!(parents.len() == 1, "octopus commits are not supported");
    let parent = parents[0].detach();
    ensure!(
        crate::worktree::is_ancestor(&repo, parent, head)?,
        "cherry-pick/revert requires a linear ancestor relationship"
    );
    let marker = if revert {
        "REVERT_HEAD"
    } else {
        "CHERRY_PICK_HEAD"
    };
    std::fs::write(repo.git_dir().join(marker), format!("{picked}\n"))?;
    let (ours, theirs) = if revert {
        (head, parent)
    } else {
        (head, picked)
    };
    let tree = if revert && head == picked {
        repo.find_commit(parent)?.tree_id()?.detach()
    } else {
        let mut merged = repo.merge_commits(
            ours,
            theirs,
            gix::merge::blob::builtin_driver::text::Labels {
                current: Some(b"HEAD".as_bstr()),
                other: Some(revision.as_bytes().as_bstr()),
                ancestor: None,
            },
            repo.tree_merge_options()?.into(),
        )?;
        ensure!(
            !merged
                .tree_merge
                .has_unresolved_conflicts(Default::default()),
            "sequencer stopped due to conflicts"
        );
        let tree = merged.tree_merge.tree.write()?.detach();
        drop(merged);
        tree
    };
    if no_commit {
        let lock = crate::write::IndexLock::acquire(&repo)?;
        let old_index = repo.index_or_empty()?;
        let index = repo.index_from_tree(tree.as_ref())?;
        return crate::worktree::apply(
            &repo,
            &old_index,
            lock,
            crate::worktree::Update {
                index,
                paths: None,
                edits: Vec::new(),
                target: None,
                require_clean: false,
                publish_index: true,
                preserve_index: false,
                overwrite_untracked: false,
                worktree_attributes: false,
                expected_head: Some(expected_head),
                file_views: None,
                metadata: Vec::new(),
                allow_merge_state: false,
                allow_sequencer_state: true,
            },
        );
    }
    let source = repo.find_commit(picked)?;
    let message = if revert {
        format!(
            "Revert \"{}\"\n\nThis reverts commit {}.",
            source.message()?.title,
            picked
        )
    } else {
        source.message_raw()?.to_string()
    };
    let text = crate::write::prepare_message(&repo, &message, false, None)?;
    ensure!(!text.is_empty(), "empty commit message");
    let target = repo.new_commit(text, tree, [head])?.id;
    let lock = crate::write::IndexLock::acquire(&repo)?;
    let old_index = repo.index_or_empty()?;
    let index = repo.index_from_tree(tree.as_ref())?;
    crate::worktree::apply(
        &repo,
        &old_index,
        lock,
        crate::worktree::Update {
            index,
            paths: None,
            edits: vec![head_edit(head, target, format!("{marker}: apply"))?],
            target: Some(target),
            require_clean: false,
            publish_index: true,
            preserve_index: false,
            overwrite_untracked: false,
            worktree_attributes: false,
            expected_head: Some(expected_head),
            file_views: None,
            metadata: Vec::new(),
            allow_merge_state: false,
            allow_sequencer_state: true,
        },
    )?;
    std::fs::remove_file(repo.git_dir().join(marker)).ok();
    crate::hooks::run(&repo, "post-commit", &[])?;
    Ok(())
}

fn head_edit(old: gix::ObjectId, next: gix::ObjectId, message: String) -> Result<RefEdit> {
    let mut edit = RefEdit::update(
        "HEAD".try_into()?,
        next,
        PreviousValue::MustExistAndMatch(old.into()),
        message,
    );
    edit.deref = true;
    Ok(edit)
}
