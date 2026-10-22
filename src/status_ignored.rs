use anyhow::Result;
use gix::{
    bstr::{BString, ByteSlice},
    dir::{EntryRef, entry, walk},
};
use std::{path::Path, sync::atomic::AtomicBool};

use crate::status_output::{Options, Row, Rows, Settings};

pub fn collect(
    repo: &gix::Repository,
    index: &gix::index::State,
    patterns: &[BString],
    options: &Options,
    settings: &Settings,
    rows: &mut Rows,
) -> Result<()> {
    let Some(mode) = options.ignored.as_deref().filter(|mode| *mode != "no") else {
        return Ok(());
    };
    if matches!(settings.untracked, gix::status::UntrackedFiles::None) {
        return Ok(());
    }
    let files =
        mode == "traditional" && matches!(settings.untracked, gix::status::UntrackedFiles::Files);
    let ignored = if files || mode == "matching" {
        walk::EmissionMode::Matching
    } else {
        walk::EmissionMode::CollapseDirectory
    };
    let selection = repo.pathspec(
        false,
        patterns.iter().map(|path| path.as_bstr()),
        false,
        index,
        gix_worktree::stack::state::attributes::Source::WorktreeThenIdMapping,
    )?;
    let mut traversal = patterns.to_vec();
    if mode == "matching" {
        // Git scopes matching directories by the common prefix, before per-file exclusions.
        let prefix = selection.search().common_prefix();
        traversal = if prefix.is_empty() {
            Vec::new()
        } else {
            let mut literal = BString::from(":(top,literal)");
            literal.extend_from_slice(prefix);
            vec![literal]
        };
    }
    let mut collector = Collector {
        rows,
        files,
        matching: mode == "matching",
        worktree: repo.workdir().expect("status requires a worktree"),
        error: None,
        selection,
        index,
        excludes: repo.excludes(
            index,
            None,
            gix_worktree::stack::state::ignore::Source::WorktreeThenIdMappingIfNotSkipped,
        )?,
    };
    // Keep ordinary status on its existing fast path; enumerate ignored paths only when requested.
    repo.dirwalk(
        index,
        traversal.iter().map(|path| path.as_bstr()),
        &AtomicBool::new(false),
        repo.dirwalk_options()?
            .emit_ignored(Some(ignored))
            .emit_untracked(walk::EmissionMode::Matching),
        &mut collector,
    )?;
    if let Some(error) = collector.error {
        return Err(error);
    }
    Ok(())
}

struct Collector<'a> {
    rows: &'a mut Rows,
    files: bool,
    matching: bool,
    worktree: &'a Path,
    error: Option<anyhow::Error>,
    selection: gix::Pathspec<'a>,
    index: &'a gix::index::State,
    excludes: gix::AttributeStack<'a>,
}

impl walk::Delegate for Collector<'_> {
    fn emit(&mut self, entry: EntryRef<'_>, _: Option<entry::Status>) -> walk::Action {
        if let Err(error) = self.collect_entry(entry) {
            self.error = Some(error);
            return std::ops::ControlFlow::Break(());
        }
        std::ops::ControlFlow::Continue(())
    }

    fn can_recurse(
        &mut self,
        entry: EntryRef<'_>,
        for_deletion: Option<walk::ForDeletionMode>,
        root: bool,
    ) -> bool {
        if matches!(entry.status, entry::Status::Ignored(_))
            && entry.disk_kind == Some(entry::Kind::Directory)
        {
            if self.files {
                let path = gix::path::from_bstr(entry.rela_path.as_ref());
                return path.is_ok_and(|path| {
                    gix::open_opts(
                        self.worktree.join(path.as_ref()).join(".git"),
                        gix::open::Options::isolated(),
                    )
                    .is_err()
                });
            }
            if self.matching || self.selected_directory(entry.rela_path.as_ref()) {
                return false;
            }
        }
        entry
            .status
            .can_recurse(entry.disk_kind, entry.pathspec_match, for_deletion, root)
    }
}

impl Collector<'_> {
    fn selected_directory(&mut self, path: &gix::bstr::BStr) -> bool {
        let prefix = self.selection.search().common_prefix();
        prefix
            .strip_prefix(path.as_bytes())
            .is_some_and(|rest| rest.is_empty() || rest.starts_with(b"/"))
            || self.selection.is_included(path, Some(true))
    }

    fn expand_directory(&mut self, path: &gix::bstr::BStr) -> Result<()> {
        let full = self.worktree.join(gix::path::from_bstr(path)?);
        for child in walkdir::WalkDir::new(full)
            .min_depth(1)
            .into_iter()
            .filter_entry(|entry| {
                entry.file_name() != ".git"
                    && (entry.depth() == 0
                        || !entry.file_type().is_dir()
                        || gix::open_opts(
                            entry.path().join(".git"),
                            gix::open::Options::isolated(),
                        )
                        .is_err())
            })
        {
            let child = child?;
            if child.file_type().is_dir() {
                continue;
            }
            let relative = gix::path::into_bstr(child.path().strip_prefix(self.worktree)?)?;
            let relative = gix::path::to_unix_separators_on_windows(relative);
            if self.index.entry_index_by_path(relative.as_bstr()).is_ok()
                || !self.selection.is_included(relative.as_bstr(), Some(false))
                || !self
                    .excludes
                    .at_entry(relative.as_bstr(), None)?
                    .is_excluded()
            {
                continue;
            }
            self.rows.insert(
                (true, relative.into_owned()),
                Row {
                    code: ['!', '!'],
                    ..Row::default()
                },
            );
        }
        Ok(())
    }

    fn collect_entry(&mut self, entry: EntryRef<'_>) -> Result<()> {
        if matches!(entry.status, entry::Status::Ignored(_))
            && entry.property != Some(entry::Property::DotGit)
            && entry.disk_kind != Some(entry::Kind::Untrackable)
        {
            let directory = entry.disk_kind.is_some_and(|kind| kind.is_dir());
            if directory
                && !self.files
                && !self.matching
                && !self.selected_directory(entry.rela_path.as_ref())
            {
                return self.expand_directory(entry.rela_path.as_ref());
            }
            if !directory
                && !self
                    .selection
                    .is_included(entry.rela_path.as_ref(), Some(false))
            {
                return Ok(());
            }
            // Matching mode reports explicitly ignored empty directories; traditional mode omits them.
            if directory && !self.matching {
                let relative = gix::path::from_bstr(entry.rela_path.as_ref())?;
                let mut has_file = false;
                for child in
                    walkdir::WalkDir::new(self.worktree.join(relative.as_ref())).min_depth(1)
                {
                    if !child?.file_type().is_dir() {
                        has_file = true;
                        break;
                    }
                }
                if !has_file {
                    return Ok(());
                }
            }
            let mut path = entry.rela_path.into_owned();
            if directory && !path.ends_with(b"/") {
                path.push(b'/');
            }
            self.rows.insert(
                (true, path),
                Row {
                    code: ['!', '!'],
                    directory,
                    ..Row::default()
                },
            );
        }
        Ok(())
    }
}
